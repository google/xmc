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

"""Playwright UI E2E verifier for the XMC Admin Panel (/admin)."""

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
EXPECTED_PAGE_TITLE = "XMC Admin Panel"
INVALID_VERSION_VALUES = ("", "-", "UNAVAILABLE")
VALID_OVERALL_BADGES = ("HEALTHY", "DEGRADED")
EXPECTED_HEALTH_GROUPS = ("Core XMC Instances", "GCP Hosted Services")
EXPECTED_COMPONENTS = (
    "ESS",
    "XID",
    "Actuator",
    "Reconciler",
    "Redis",
    "Spanner ESS",
    "Spanner XID",
)
VALID_COMPONENT_STATUSES = ("Running", "Online", "Connected", "Degraded")
EXPECTED_CONFIG_GROUPS = (
    "Experiment State Server Config",
    "Actuator Config",
    "Reconciler Config",
    "XID Service Config",
    "Health Service Config",
)
Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError


def _read_trimmed_text(session: Session, selector: str) -> str:
    """Returns the trimmed text content of the first element matching selector."""
    return (session.page.locator(selector).first.text_content() or "").strip()


def assert_system_info_and_header(session: Session, timeout_ms: int = 30000) -> dict[str, str]:
    """CUJ 1: Verifies Admin Panel page header, System Info versions/schemas, and footer."""
    session.wait_for_selector_with_retry(sel.SYSTEM_INFO_CARD_SEL, timeout_ms)
    title = _read_trimmed_text(session, sel.ADMIN_PAGE_TITLE_SEL)
    timestamp = _read_trimmed_text(session, sel.ADMIN_TIMESTAMP_BADGE_SEL)
    footer = _read_trimmed_text(session, sel.ADMIN_FOOTER_SEL)
    if title != EXPECTED_PAGE_TITLE or not timestamp.startswith("Last updated:"):
        raise UIError(f"Unexpected Admin Panel header: title='{title}', timestamp='{timestamp}'")
    if not footer.startswith("Build Version:") or "N/A" in footer:
        raise UIError(f"Unexpected Admin Panel footer build version: '{footer}'")
    info = {
        "app_version": _read_trimmed_text(session, sel.APP_VERSION_VAL_SEL),
        "ess_schema": _read_trimmed_text(session, sel.ESS_SCHEMA_VAL_SEL),
        "xid_schema": _read_trimmed_text(session, sel.XID_SCHEMA_VAL_SEL),
    }
    if bad := [k for k, v in info.items() if v in INVALID_VERSION_VALUES]:
        raise UIError(f"Invalid or unavailable System Info fields {bad}: {info}")
    print(f"  [✓] Verified System Info & Header: {info}, {footer}")
    return info


def _collect_component_statuses(session: Session) -> dict[str, str]:
    """Extracts component name -> status label across all rendered health group cards."""
    rows = session.page.locator(sel.HEALTH_ITEM_ROW_SEL)
    statuses: dict[str, str] = {}
    for i in range(rows.count()):
        row = rows.nth(i)
        name = (row.locator(sel.HEALTH_ITEM_NAME_SEL).first.text_content() or "").strip()
        status = (row.locator(sel.HEALTH_ITEM_STATUS_SEL).first.text_content() or "").strip()
        if name:
            statuses[name] = status
    return statuses


def _verify_health_groups_and_items(session: Session, timeout_ms: int) -> dict[str, str]:
    """Verifies health group titles and component status rows in XMC Services Health."""
    session.page.wait_for_selector(sel.HEALTH_ITEM_ROW_SEL, timeout=timeout_ms)
    raw_groups = session.page.locator(sel.HEALTH_GROUP_TITLE_SEL).all_inner_texts()
    groups = [g.strip() for g in raw_groups if g.strip()]
    if missing_groups := [g for g in EXPECTED_HEALTH_GROUPS if not any(g in t for t in groups)]:
        raise UIError(f"Missing expected health groups {missing_groups} in {groups}")
    statuses = _collect_component_statuses(session)
    if missing_comps := [c for c in EXPECTED_COMPONENTS if c not in statuses]:
        raise UIError(f"Missing expected health components {missing_comps} in {statuses}")
    if bad := {k: v for k, v in statuses.items() if v not in VALID_COMPONENT_STATUSES}:
        raise UIError(f"Unexpected component health status labels: {bad}")
    return statuses


def assert_services_health_and_refresh(session: Session, timeout_ms: int = 30000) -> dict[str, Any]:
    """CUJ 2: Verifies XMC Services Health groups, component statuses, and Refresh action."""
    statuses = _verify_health_groups_and_items(session, timeout_ms)
    badge = _read_trimmed_text(session, sel.HEALTH_OVERALL_BADGE_SEL)
    if badge not in VALID_OVERALL_BADGES:
        raise UIError(f"Expected overall health badge in {VALID_OVERALL_BADGES}, got '{badge}'.")
    with session.page.expect_response(lambda r: "/v1/health/status" in r.url, timeout=timeout_ms):
        session.page.locator(sel.HEALTH_REFRESH_BTN_SEL).first.click()
    session.page.wait_for_selector(sel.HEALTH_REFRESH_READY_SEL, timeout=timeout_ms)
    last_checked = _read_trimmed_text(session, sel.HEALTH_LAST_CHECKED_SEL)
    if not last_checked.startswith("Last checked:"):
        raise UIError(f"Expected 'Last checked:' timestamp after refresh, got '{last_checked}'.")
    print(f"  [✓] Verified Services Health ({badge}) & Refresh: {statuses}")
    return {"overall_badge": badge, "components": statuses}


def _verify_config_group_collapse_toggle(session: Session, timeout_ms: int) -> None:
    """Clicks the first configuration group header to collapse and re-expand its rows."""
    first_group = session.page.locator(sel.CONFIG_GROUP_ROW_SEL).first
    for expanded, expected_icon in (("false", "+"), ("true", "−")):
        first_group.click()
        session.page.wait_for_selector(
            f"{sel.CONFIG_GROUP_ROW_SEL}[aria-expanded='{expanded}']", timeout=timeout_ms
        )
        icon = (first_group.locator(sel.CONFIG_GROUP_ICON_SEL).first.text_content() or "").strip()
        if icon != expected_icon:
            raise UIError(f"Expected toggle icon '{expected_icon}', got '{icon}'.")


def _verify_config_search_filter(session: Session, sample_key: str, timeout_ms: int) -> None:
    """Exercises positive key filtering, no-match empty state, and filter reset."""
    search_input = session.page.locator(sel.CONFIG_FILTER_INPUT_SEL).first
    search_input.fill(sample_key)
    session.page.locator(sel.CONFIG_ROW_SEL).filter(has_text=sample_key).first.wait_for(
        state="visible", timeout=timeout_ms
    )
    search_input.fill(NO_MATCH_SENTINEL)
    session.page.wait_for_selector(sel.CONFIG_NO_RESULTS_SEL, timeout=timeout_ms)
    if session.page.locator(sel.CONFIG_ROW_SEL).count() != 0:
        raise UIError("Expected 0 config rows when filtering by no-match sentinel.")
    search_input.fill("")
    session.page.wait_for_selector(sel.CONFIG_NO_RESULTS_SEL, state="hidden", timeout=timeout_ms)


def assert_config_entries_and_filter(session: Session, timeout_ms: int = 30000) -> list[str]:
    """CUJ 3: Verifies Configuration Entries groups, collapse/expand, and search filtering."""
    session.page.wait_for_selector(sel.CONFIG_ROW_SEL, timeout=timeout_ms)
    raw_groups = session.page.locator(sel.CONFIG_GROUP_NAME_SEL).all_inner_texts()
    groups = [g.strip() for g in raw_groups if g.strip()]
    if missing := [g for g in EXPECTED_CONFIG_GROUPS if g not in groups]:
        raise UIError(f"Missing expected config groups {missing} in {groups}")
    sample_key = _read_trimmed_text(session, f"{sel.CONFIG_ROW_SEL} {sel.CONFIG_KEY_CELL_SEL}")
    sample_val = _read_trimmed_text(session, f"{sel.CONFIG_ROW_SEL} {sel.CONFIG_VAL_CELL_SEL}")
    if not sample_key or not sample_val:
        raise UIError(f"Expected non-empty first config row, got '{sample_key}'='{sample_val}'")
    _verify_config_group_collapse_toggle(session, timeout_ms)
    _verify_config_search_filter(session, sample_key, timeout_ms)
    print(
        f"  [✓] Verified {len(groups)} config groups, collapse toggle, and filter ('{sample_key}')"
    )
    return groups


def verify_admin_panel_ui(session: Session) -> dict[str, Any]:
    """Executes the Admin Panel Playwright UI E2E verification suite (/admin)."""
    session.navigate_to_route("/admin", sel.ADMIN_PANEL_ROOT_SEL, "ui_admin_step1_landing.png")
    sys_info = assert_system_info_and_header(session)
    health_info = assert_services_health_and_refresh(session)
    session.take_screenshot("ui_admin_step2_services_health.png")
    config_groups = assert_config_entries_and_filter(session)
    session.take_screenshot("ui_admin_step3_config_entries.png")
    session.assert_no_runtime_errors("admin_panel")
    rows = [
        ("UI Base URL", session.ui_base_url),
        (
            "App / Schema Versions",
            f"{sys_info['app_version']} (ESS={sys_info['ess_schema']}, XID={sys_info['xid_schema']})",
        ),
        ("Overall Health", health_info["overall_badge"]),
        ("Verified Config Groups", ", ".join(config_groups)),
    ]
    rep.print_success_summary("ADMIN PANEL UI E2E PASSED!", rows)
    return {"system_info": sys_info, **health_info, "config_groups": config_groups}


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone Admin Panel UI E2E verification."""
    cli.validate_argv(argv)
    with ess.handle_verification_errors(), Session.from_flags() as session:
        verify_admin_panel_ui(session)


if __name__ == "__main__":
    cli.run_main(main)
