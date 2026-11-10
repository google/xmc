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

"""Playwright UI E2E verifier for the XMC Resource Observability Dashboard (/resources)."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any, TypeAlias

from tools.nightly_tests.verification import (
    ess_session as ess,
    resource_observability_parsing as rod_parsing,
    ui_css_selectors as sel,
    ui_entities_validator,
    verification_report as rep,
    verifier_cli as cli,
    xmc_entities_validator,
)

FLAGS = cli.FLAGS

EXPECTED_FIRST_CARD_FLAVOR = "cpu"
DEFAULT_WORK_UNITS, VELOCITY_TIMEFRAMES = 3, ("1h", "24h", "7d")
Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError


def expected_work_unit_ids() -> tuple[str, ...]:
    """Returns WorkUnit IDs "1".."N" for the resolved --expected_work_units count."""
    return tuple(str(i) for i in range(1, cli.resolve_expected_work_units(DEFAULT_WORK_UNITS) + 1))


def _extract_and_verify_queue_titles(session: Session, expected_q: str | None) -> list[str]:
    """Extracts rendered queue card titles and verifies expected cluster queues are present."""
    raw = session.page.locator(sel.QUEUE_TITLE_SEL).all_inner_texts()
    titles = [t.strip() for t in raw if t.strip()]
    if missing := [q for q in rod_parsing.EXPECTED_CLUSTER_QUEUES if q not in titles]:
        raise UIError(f"Missing expected queues {missing} in rendered queues: {titles}")
    if expected_q and not (
        {expected_q, rod_parsing.resolve_cluster_queue(expected_q)} & set(titles)
    ):
        raise UIError(f"Missing '{expected_q}' in {titles}")
    return titles


def assert_queue_cards(session: Session, expected_queue: str | None = None) -> list[str]:
    """Asserts that cluster queue cards and quota flavors render on /resources."""
    session.wait_for_selector_with_retry(sel.QUEUE_CARD_SEL, 30000)
    titles = _extract_and_verify_queue_titles(session, expected_queue)
    first_card = session.page.locator(sel.QUEUE_CARD_SEL).first
    flavor = (first_card.locator(sel.FLAVOR_NAME_SEL).first.text_content() or "").strip()
    if flavor != EXPECTED_FIRST_CARD_FLAVOR:
        raise UIError(f"Expected flavor '{EXPECTED_FIRST_CARD_FLAVOR}', got '{flavor}'.")
    print(f"  [✓] Verified {len(titles)} queue card(s): {titles} (flavor={flavor})")
    return titles


def _select_queue_card_by_name(session: Session, queue_name: str, timeout_ms: int = 15000) -> str:
    """Clicks a specific cluster queue card and waits for the Jobs in the Queue header to sync."""
    session.page.locator(sel.QUEUE_CARD_SEL).filter(has_text=queue_name).first.click()
    session.settle_and_wait_fonts()
    header_loc = session.page.locator(sel.JOBS_PANE_QUEUE_NAME_SEL).first
    header_loc.wait_for(state="visible", timeout=timeout_ms)
    actual_name = (header_loc.text_content() or "").strip()
    if actual_name != queue_name:
        raise UIError(f"Expected '{queue_name}', got '{actual_name}'.")
    session.page.wait_for_selector(sel.JOBS_CONTENT_SEL, timeout=timeout_ms)
    return actual_name


def _collect_xid_work_unit_positions(
    session: Session, xid: str, expected_ids: tuple[str, ...]
) -> dict[str, str]:
    """Returns workUnitId -> position text for XID <xid> across paginator pages."""
    positions: dict[str, str] = {}
    link_sel = sel.JOB_EXPERIMENT_LINK_SEL_TMPL.format(xid=xid)
    for _ in range(5):
        rows = session.page.locator(sel.JOBS_TABLE_ROW_SEL).filter(
            has=session.page.locator(link_sel)
        )
        for i in range(rows.count()):
            row = rows.nth(i)
            wid = (row.locator(sel.JOB_WORKUNIT_CELL_SEL).first.text_content() or "").strip()
            pos = (row.locator(sel.JOB_POSITION_CELL_SEL).first.text_content() or "").strip()
            if wid:
                positions[wid] = pos
        next_btn = session.page.locator(sel.JOBS_NEXT_PAGE_BTN_SEL)
        if all(w in positions for w in expected_ids) or next_btn.count() == 0:
            break
        next_btn.first.click()
        session.settle_and_wait_fonts()
        session.page.wait_for_selector(sel.JOBS_CONTENT_SEL, timeout=15000)
    return positions


def _verify_work_units_in_jobs_table(session: Session, cq: str, alt: str, xid: str | None) -> None:
    """Polls the Jobs in the Queue table until all expected work units have valid positions."""
    if not xid:
        return
    norm_xid = ui_entities_validator.parse_numeric_xid(xid)
    expected_ids = expected_work_unit_ids()
    for attempt in range(12):
        pos = _collect_xid_work_unit_positions(session, norm_xid, expected_ids)
        if all(rod_parsing.is_valid_queue_position(pos.get(w, "")) for w in expected_ids):
            print(f"  [✓] Verified '{cq}' Jobs table has XID {norm_xid} work units: {pos}")
            return
        if attempt < 11:
            _select_queue_card_by_name(session, alt)
            session.settle_and_wait_fonts(1500)
            _select_queue_card_by_name(session, cq)
    raise UIError(f"Missing XID {norm_xid} work units {expected_ids} in '{cq}': {pos}")


def assert_queue_selection_and_jobs_pane(
    session: Session, queues: Sequence[str], expected_q: str | None = None, xid: str | None = None
) -> str:
    """Clicks queue cards and verifies the Jobs in the Queue side pane and active work units."""
    target_cq = rod_parsing.resolve_cluster_queue(expected_q)
    alt_cq = next((q for q in queues if q != target_cq), target_cq)
    _select_queue_card_by_name(session, alt_cq)
    actual_name = _select_queue_card_by_name(session, target_cq)
    _verify_work_units_in_jobs_table(session, target_cq, alt_cq, xid)
    print(f"  [✓] Verified Jobs in the Queue side pane synchronized to '{actual_name}'")
    return actual_name


def _select_primary_resource(session: Session, timeout_ms: int) -> str:
    """Selects CPU in Smart Resource Finder and waits for predictability metrics to render."""
    session.page.locator(sel.RESOURCE_SELECT_SEL).first.click()
    session.page.wait_for_selector(sel.RESOURCE_OPTION_SEL, timeout=timeout_ms)
    options = session.page.locator(sel.RESOURCE_OPTION_SEL)
    if options.count() < 2:
        raise UIError("Missing selectable resource mat-option.")
    cpu = options.filter(has_text="CPU").first
    opt = cpu if cpu.count() > 0 else options.nth(1)
    name = (opt.text_content() or "").strip()
    opt.click()
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.PREDICTABILITY_CONTENT_SEL, timeout=timeout_ms)
    return name


def _verify_expected_queue_positive_7d_velocity(session: Session, q: str | None, ms: int) -> None:
    """Verifies that the expected queue card displays positive velocity under the 7d period."""
    target_cq = rod_parsing.resolve_cluster_queue(q)
    card = session.page.locator(sel.QUEUE_CARD_SEL).filter(has_text=target_cq).first
    card.locator(sel.PREDICTABILITY_STATS_GRID_SEL).first.wait_for(state="visible", timeout=ms)
    vals = [t.strip() for t in card.locator(sel.PREDICTABILITY_METRIC_VAL_SEL).all_inner_texts()]
    if not any(rod_parsing.parse_velocity_rate(v) > 0 for v in vals[:2]):
        raise UIError(f"Expected positive 7d velocity on '{target_cq}', got metrics: {vals}")
    print(f"  [✓] Verified positive 7d velocity on '{target_cq}': {vals[:2]}")


def assert_smart_resource_finder_and_predictability(
    session: Session, expected_q: str | None = None
) -> str:
    """Exercises Smart Resource Finder controls, predictability metrics, and timeframe toggles."""
    session.page.wait_for_selector(sel.PREDICTABILITY_EMPTY_SEL, timeout=15000)
    resource_name = _select_primary_resource(session, 15000)
    for tf in VELOCITY_TIMEFRAMES:
        session.page.locator(sel.VELOCITY_PILL_SEL_TMPL.format(tf=tf)).first.click()
        session.page.wait_for_selector(sel.ACTIVE_PILL_SEL_TMPL.format(tf=tf), timeout=15000)
    _verify_expected_queue_positive_7d_velocity(session, expected_q, 15000)
    print(f"  [✓] Verified Smart Resource Finder & Predictability for resource '{resource_name}'")
    return resource_name


def assert_subnav_tabs_switching(session: Session, timeout_ms: int = 15000) -> None:
    """Switches between ROD subnavigation tabs and verifies view transitions."""
    session.page.locator(sel.AVAILABILITY_TAB_SEL).first.click()
    session.page.wait_for_selector(sel.WIP_BANNER_SEL, timeout=timeout_ms)
    session.take_screenshot("ui_rod_step4_resource_availability.png")
    session.page.locator(sel.SMART_FINDER_TAB_SEL).first.click()
    session.page.wait_for_selector(sel.SMART_FINDER_ROOT_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()
    print("  [✓] Verified ROD subnavigation tab switching")


def verify_resource_dashboard_ui(
    session: Session, expected_q: str | None = None, xid: str | None = None
) -> dict[str, Any]:
    """Executes the Resource Observability Dashboard UI E2E verification suite."""
    session.navigate_to_route("/resources", sel.DASHBOARD_ROOT_SEL, "ui_rod_step1_landing.png")
    queues = assert_queue_cards(session, expected_queue=expected_q)
    selected_q = assert_queue_selection_and_jobs_pane(session, queues, expected_q, xid)
    session.take_screenshot("ui_rod_step2_queue_selection.png")
    selected_res = assert_smart_resource_finder_and_predictability(session, expected_q)
    session.take_screenshot("ui_rod_step3_smart_resource_finder.png")
    assert_subnav_tabs_switching(session)
    session.assert_no_runtime_errors("resource_dashboard")
    rows = [("UI Base URL", session.ui_base_url), ("Verified Queues", ", ".join(queues))]
    rep.print_success_summary("ROD UI E2E PASSED!", rows)
    return {"queues": queues, "selected_queue": selected_q, "selected_resource": selected_res}


def _stop_experiment_work_units(xid: str | None, endpoint: str | None) -> None:
    """Stops active work units for the launched experiment when a valid numeric xid is provided."""
    if xid and xid.isdigit():
        with ess.ess_channel_context(endpoint) as (stub, _):
            xmc_entities_validator.stop_work_units(stub, xid)


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone ROD UI E2E verification."""
    cli.validate_argv(argv)
    get = cli.get_flag
    xid = cli.parse_xid(get(FLAGS, "xid"), required=False) or None
    with ess.handle_verification_errors(), Session.from_flags(FLAGS) as session:
        try:
            verify_resource_dashboard_ui(session, get(FLAGS, "expected_queue"), xid)
        finally:
            _stop_experiment_work_units(xid, get(FLAGS, "endpoint"))


if __name__ == "__main__":
    cli.run_main(main)
