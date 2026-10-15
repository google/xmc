# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Playwright UI E2E verifier for the XMC Dashboards tab (/dashboards and /dashboards/:id)."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any, TypeAlias

from tools.nightly_tests.verification import (
    ess_session as ess,
    ui_css_selectors as sel,
    ui_entities_validator,
    verification_report as rep,
    verifier_cli as cli,
)

NO_MATCH_SENTINEL = "__nightly_no_match_sentinel__"
EXPECTED_CHART_CARDS, EXPECTED_SUMMARY_BOXES, EXPECTED_PLOT_CANVASES = 2, 3, 3
MIN_CANVAS_DIM = 100
Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError


def _verify_matching_dashboard_row(session: Session, norm_xid: str, slug: str) -> str:
    """Verifies the filtered dashboard row displays the expected title, resource name, and count."""
    rows = session.page.locator(sel.DASHBOARD_ROW_SEL).filter(has_text=slug)
    if rows.count() == 0:
        raise UIError(f"Missing dashboard row for '{slug}' in /dashboards table.")
    row = rows.first
    title = (row.locator(sel.DASHBOARD_TITLE_TEXT_SEL).first.text_content() or "").strip()
    res_name = (row.locator(sel.DASHBOARD_RESOURCE_NAME_SEL).first.text_content() or "").strip()
    badge = (row.locator(sel.DASHBOARD_BADGE_COUNT_SEL).first.text_content() or "").strip()
    if f"Experiment {norm_xid}" not in title or res_name != f"dashboards/{slug}":
        raise UIError(f"Unexpected dashboard row metadata: title='{title}', name='{res_name}'")
    if f"{EXPECTED_CHART_CARDS} experiments" not in badge:
        raise UIError(f"Expected '{EXPECTED_CHART_CARDS} experiments' badge, got '{badge}'.")
    return title


def _verify_empty_filter_and_reset(session: Session, slug: str, timeout_ms: int) -> None:
    """Exercises the no-match search filter empty state and resets back to the target slug."""
    search_input = session.page.locator(sel.DASHBOARD_FILTER_INPUT_SEL).first
    search_input.fill(NO_MATCH_SENTINEL)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.DASHBOARD_EMPTY_STATE_SEL, timeout=timeout_ms)
    if session.page.locator(sel.DASHBOARD_ROW_SEL).count() != 0:
        raise UIError("Expected 0 dashboard rows when filtering by no-match sentinel.")
    session.page.locator(sel.DASHBOARD_CLEAR_FILTER_BTN_SEL).first.click()
    session.settle_and_wait_fonts()
    search_input.fill(slug)
    session.settle_and_wait_fonts()


def assert_dashboards_list_and_filter(
    session: Session, xid: str | int, timeout_ms: int = 30000
) -> str:
    """CUJ 1: Navigates to /dashboards, exercises search filtering, and opens the dashboard."""
    norm_xid = ui_entities_validator.parse_numeric_xid(xid)
    slug = f"exp_{norm_xid}_overview"
    session.navigate_to_route("/dashboards", sel.DASHBOARDS_LIST_ROOT_SEL, "ui_dash_step1_list.png")
    session.wait_for_selector_with_retry(sel.DASHBOARD_ROW_SEL, timeout_ms)
    session.page.locator(sel.DASHBOARD_FILTER_INPUT_SEL).first.fill(slug)
    session.settle_and_wait_fonts()
    title = _verify_matching_dashboard_row(session, norm_xid, slug)
    _verify_empty_filter_and_reset(session, slug, timeout_ms)
    row = session.page.locator(sel.DASHBOARD_ROW_SEL).filter(has_text=slug).first
    row.locator(sel.DASHBOARD_LINK_SEL).first.click()
    session.page.wait_for_selector(sel.DASHBOARDS_DETAIL_ROOT_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()
    print(f"  [✓] Verified /dashboards list, search filter, and navigation to '{slug}' ({title})")
    return slug


def _wait_for_hydrated_chart_cards(session: Session, timeout_ms: int) -> int:
    """Waits for all expected chart cards to render and finish loading without errors."""
    cards = session.page.locator(sel.CHART_CARD_SEL)
    cards.nth(EXPECTED_CHART_CARDS - 1).wait_for(state="visible", timeout=timeout_ms)
    for i in range(EXPECTED_CHART_CARDS):
        cards.nth(i).locator(sel.CHART_SPINNER_SEL).wait_for(state="detached", timeout=timeout_ms)
    if session.page.locator(sel.CHART_ERROR_SEL).count() != 0:
        err = (session.page.locator(sel.CHART_ERROR_SEL).first.text_content() or "").strip()
        raise UIError(f"Chart card rendered error state: '{err}'")
    return cards.count()


def _verify_chart_headers_and_summary_boxes(
    session: Session, norm_xid: str, timeout_ms: int = 30000
) -> list[str]:
    """Verifies chart header work unit deep-links and hydrated KPI summary box values."""
    headers = session.page.locator(sel.CHART_HEADER_LINK_SEL)
    headers.nth(EXPECTED_CHART_CARDS - 1).wait_for(state="visible", timeout=timeout_ms)
    for i in range(headers.count()):
        text = (headers.nth(i).text_content() or "").strip()
        href = headers.nth(i).get_attribute("href") or ""
        if f"XID/{norm_xid}" not in text or f"/experiments/{norm_xid}/workUnits/1" not in href:
            raise UIError(f"Unexpected chart header #{i}: text='{text}', href='{href}'")
    hydrated = session.page.locator(f"{sel.SUMMARY_BOX_SEL} {sel.SUMMARY_VALUE_SEL}").filter(
        has_not_text="—"
    )
    hydrated.nth(EXPECTED_SUMMARY_BOXES - 1).wait_for(state="visible", timeout=timeout_ms)
    vals = [t.strip() for t in hydrated.all_inner_texts() if t.strip()]
    if not any(v.endswith("%") for v in vals):
        raise UIError(f"Expected accuracy summary box formatted with '%', got: {vals}")
    return vals


def _verify_echarts_plot_canvases(session: Session, timeout_ms: int) -> int:
    """Asserts that all ECharts curve plots render non-empty HTML5 canvas elements."""
    canvases = session.page.locator(sel.PLOT_CANVAS_SEL)
    for i in range(EXPECTED_PLOT_CANVASES):
        canvases.nth(i).wait_for(state="visible", timeout=timeout_ms)
    session.settle_and_wait_fonts()
    count = canvases.count()
    for i in range(count):
        box = canvases.nth(i).bounding_box()
        w, h = (box["width"], box["height"]) if box else (0.0, 0.0)
        if w < MIN_CANVAS_DIM or h < MIN_CANVAS_DIM:
            raise UIError(f"ECharts canvas #{i} has invalid dimensions: width={w}, height={h}")
    return count


def assert_dashboard_detail_and_plots(
    session: Session, xid: str | int, timeout_ms: int = 30000
) -> dict[str, Any]:
    """CUJ 2: Verifies hydrated KPI summary boxes, header deep-links, and ECharts canvases."""
    norm_xid = ui_entities_validator.parse_numeric_xid(xid)
    title_loc = session.page.locator(sel.DASHBOARD_DETAIL_TITLE_SEL).first
    title_loc.wait_for(state="visible", timeout=timeout_ms)
    title = (title_loc.text_content() or "").strip()
    if f"Experiment {norm_xid}" not in title:
        raise UIError(f"Expected 'Experiment {norm_xid}' in dashboard title, got '{title}'.")
    card_count = _wait_for_hydrated_chart_cards(session, timeout_ms)
    summary_vals = _verify_chart_headers_and_summary_boxes(session, norm_xid, timeout_ms)
    canvas_count = _verify_echarts_plot_canvases(session, timeout_ms)
    session.take_screenshot("ui_dash_step2_detail_plots.png")
    print(
        f"  [✓] Verified '{title}': {card_count} chart cards, "
        f"summaries={summary_vals}, {canvas_count} ECharts canvases"
    )
    return {
        "title": title,
        "cards": card_count,
        "summaries": summary_vals,
        "canvases": canvas_count,
    }


def verify_metrics_dashboard_ui(session: Session, xid: str | int) -> dict[str, Any]:
    """Executes CUJ 1 and CUJ 2 Playwright UI E2E verification for the Dashboards tab."""
    slug = assert_dashboards_list_and_filter(session, xid)
    detail = assert_dashboard_detail_and_plots(session, xid)
    session.assert_no_runtime_errors("metrics_dashboard")
    rows = [
        ("UI Base URL", session.ui_base_url),
        ("Dashboard Slug", slug),
        ("Dashboard Title", detail["title"]),
        ("Summary Values", ", ".join(detail["summaries"])),
    ]
    rep.print_success_summary("METRICS DASHBOARD UI E2E PASSED!", rows)
    return {"dashboard_slug": slug, **detail}


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone Metrics Dashboard UI E2E verification."""
    cli.validate_argv(argv)
    xid = cli.parse_xid(cli.get_flag(cli.FLAGS, "xid"))
    with ess.handle_verification_errors(), Session.from_flags() as session:
        verify_metrics_dashboard_ui(session, xid)


if __name__ == "__main__":
    cli.run_main(main)
