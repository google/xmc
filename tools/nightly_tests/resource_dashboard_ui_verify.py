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

from absl import flags

from tools.nightly_tests import ui_entities_validator
from tools.nightly_tests.verification import (
    endpoint_resolution,
    ess_session,
    verification_report,
    verifier_cli,
)

FLAGS = flags.FLAGS
flags.DEFINE_string("ui_base_url", None, "Base URL of the XMC Web UI", allow_override=True)
flags.DEFINE_string("auth_token", None, "Optional Bearer/IAP auth token", allow_override=True)
flags.DEFINE_bool("headless", True, "Run browser in headless mode", allow_override=True)
flags.DEFINE_string("screenshots_dir", None, "Diagnostic screenshots dir", allow_override=True)

VELOCITY_TIMEFRAMES: tuple[str, ...] = ("1h", "24h", "7d")
Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError

DASHBOARD_ROOT_SEL = "xmc-resource-dashboard"
QUEUE_CARD_SEL = "xmc-rod-queue-card .queue-card"
QUEUE_TITLE_SEL = "xmc-rod-queue-card .queue-title"
FLAVOR_NAME_SEL = ".capacity-box .flavor-main-name"
STATUS_BADGE_SEL = ".status-badge"
COPY_FLAG_BTN_SEL = "button.copy-flag-btn"
COPIED_FLAG_BTN_SEL = 'xmc-rod-queue-card button.copy-flag-btn:has-text("Copied")'
JOBS_PANE_QUEUE_NAME_SEL = "xmc-queue-jobs-table .target-queue-name"
JOBS_CONTENT_SEL = "xmc-queue-jobs-table .jobs-mat-table, xmc-queue-jobs-table .jobs-empty-state"
PREDICTABILITY_EMPTY_SEL = ".predictability-empty-state"
PREDICTABILITY_CONTENT_SEL = ".predictability-stats-grid, .predictability-spinner-container"
RESOURCE_SELECT_SEL = "xmc-smart-resource-finder mat-select.query-select-box"
RESOURCE_OPTION_SEL = "mat-option"
RESOURCE_AMOUNT_INPUT_SEL = "xmc-smart-resource-finder input.query-amount-input"
ADD_RESOURCE_BTN_SEL = "xmc-smart-resource-finder button.add-resource-btn"
REMOVE_RESOURCE_BTN_SEL = "xmc-smart-resource-finder button.remove-resource-btn"
RESOURCE_QUERY_ROW_SEL = "xmc-smart-resource-finder .query-row"
QUEUE_LIST_EMPTY_SEL = "xmc-rod-queue-list .empty-state"
VELOCITY_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill:has-text("{tf}")'
ACTIVE_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill.active-toggle:has-text("{tf}")'
AVAILABILITY_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Resource availability")'
SMART_FINDER_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Smart resource finder")'
WIP_BANNER_SEL = "xmc-wip-banner"
SMART_FINDER_ROOT_SEL = "xmc-smart-resource-finder"


def _wait_for_queue_cards_with_retry(session: Session, timeout_ms: int) -> None:
    """Waits for queue cards to render, reloading once if initial load hit a cold-start 500."""
    try:
        session.page.wait_for_selector(QUEUE_CARD_SEL, timeout=5000)
    except Exception:
        session.http_errors.clear()
        session.page.reload(wait_until="networkidle", timeout=timeout_ms)
        session.page.wait_for_selector(QUEUE_CARD_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()


def _extract_and_verify_queue_titles(session: Session, expected_queue: str | None) -> list[str]:
    """Extracts rendered queue card titles and verifies the expected queue is present."""
    raw_titles = session.page.locator(QUEUE_TITLE_SEL).all_inner_texts()
    titles = [t.strip() for t in raw_titles if t and t.strip()]
    if not titles:
        raise UIError("No cluster queue cards on /resources.")
    cq = expected_queue.replace("-queue", "-cluster-queue") if expected_queue else ""
    if expected_queue and not ({expected_queue, cq} & set(titles)):
        raise UIError(f"Missing '{expected_queue}' in {titles}")
    return titles


def _verify_first_card_capacity_and_status(first_card: Any) -> tuple[str, str]:
    """Verifies that the first queue card renders a non-empty quota flavor and status badge."""
    flavor = (first_card.locator(FLAVOR_NAME_SEL).first.text_content() or "").strip()
    badge = (first_card.locator(STATUS_BADGE_SEL).first.text_content() or "").strip()
    if not flavor or not badge:
        raise UIError(f"Missing flavor/badge: {flavor}/{badge}")
    return flavor, badge


def _verify_copy_flag_action(session: Session, first_card: Any, timeout_ms: int) -> None:
    """Clicks the Copy flag button on the first card and verifies the 'Copied' state transition."""
    copy_btn = first_card.locator(COPY_FLAG_BTN_SEL).first
    copy_btn.click()
    session.page.wait_for_selector(COPIED_FLAG_BTN_SEL, timeout=timeout_ms)
    btn_text = (copy_btn.text_content() or "").strip()
    if "Copied" not in btn_text:
        raise UIError(f"Expected 'Copied', got '{btn_text}'.")


def assert_queue_cards(
    session: Session, expected_queue: str | None = None, timeout_ms: int = 30000
) -> list[str]:
    """Asserts that cluster queue cards, quota flavors, and copy-flag actions render on /resources."""
    print("\n[ROD UI] Asserting cluster queue cards and capacity metadata...")
    _wait_for_queue_cards_with_retry(session, timeout_ms)
    titles = _extract_and_verify_queue_titles(session, expected_queue)
    first_card = session.page.locator(QUEUE_CARD_SEL).first
    flavor, badge = _verify_first_card_capacity_and_status(first_card)
    _verify_copy_flag_action(session, first_card, timeout_ms)
    print(f"  [✓] Verified {len(titles)} queue card(s): {titles} (flavor={flavor}, status={badge})")
    return titles


def assert_queue_selection_and_jobs_pane(
    session: Session, queue_titles: Sequence[str], timeout_ms: int = 15000
) -> str:
    """Clicks a queue card and verifies that the Jobs in the Queue side pane updates."""
    target_idx = 1 if len(queue_titles) > 1 else 0
    expected_name = queue_titles[target_idx]
    print(f"\n[ROD UI] Selecting queue card #{target_idx + 1} ('{expected_name}')...")
    session.page.locator(QUEUE_CARD_SEL).nth(target_idx).click()
    session.settle_and_wait_fonts()
    header_loc = session.page.locator(JOBS_PANE_QUEUE_NAME_SEL).first
    header_loc.wait_for(state="visible", timeout=timeout_ms)
    actual_name = (header_loc.text_content() or "").strip()
    if actual_name != expected_name:
        raise UIError(f"Expected '{expected_name}', got '{actual_name}'.")
    session.page.wait_for_selector(JOBS_CONTENT_SEL, timeout=timeout_ms)
    print(f"  [✓] Verified Jobs in the Queue side pane synchronized to '{actual_name}'")
    return actual_name


def _assert_initial_predictability_empty_state(session: Session, timeout_ms: int) -> None:
    """Verifies that the predictability column starts in its empty prompt state."""
    session.page.wait_for_selector(PREDICTABILITY_EMPTY_SEL, timeout=timeout_ms)
    if session.page.locator(PREDICTABILITY_EMPTY_SEL).count() == 0:
        raise UIError(f"Missing {PREDICTABILITY_EMPTY_SEL}.")


def _select_primary_resource_and_amount(session: Session, timeout_ms: int) -> str:
    """Selects the first resource type in Smart Resource Finder and sets amount to 1."""
    session.page.locator(RESOURCE_SELECT_SEL).first.click()
    session.page.wait_for_selector(RESOURCE_OPTION_SEL, timeout=timeout_ms)
    options = session.page.locator(RESOURCE_OPTION_SEL)
    if options.count() < 2:
        raise UIError("Missing selectable resource mat-option.")
    target_option = options.nth(1)
    resource_name = (target_option.text_content() or "").strip()
    target_option.click()
    session.page.locator(RESOURCE_AMOUNT_INPUT_SEL).first.fill("1")
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(PREDICTABILITY_CONTENT_SEL, timeout=timeout_ms)
    return resource_name


def _verify_velocity_timeframe_pills(session: Session, timeout_ms: int) -> None:
    """Clicks each velocity timeframe toggle pill and verifies it becomes active."""
    for tf in VELOCITY_TIMEFRAMES:
        pill = session.page.locator(VELOCITY_PILL_SEL_TMPL.format(tf=tf)).first
        pill.click()
        session.page.wait_for_selector(ACTIVE_PILL_SEL_TMPL.format(tf=tf), timeout=timeout_ms)
        if "active-toggle" not in (pill.get_attribute("class") or ""):
            raise UIError(f"Pill '{tf}' missing 'active-toggle'.")


def _verify_multi_resource_row_add_and_remove(session: Session) -> None:
    """Adds a secondary resource filter row and removes it."""
    add_btn = session.page.locator(ADD_RESOURCE_BTN_SEL).first
    if add_btn.count() > 0 and add_btn.is_enabled():
        add_btn.click()
        session.settle_and_wait_fonts(200)
        row_count = session.page.locator(RESOURCE_QUERY_ROW_SEL).count()
        if row_count < 2:
            raise UIError(f"Expected >= 2 rows, got {row_count}.")
        session.page.locator(REMOVE_RESOURCE_BTN_SEL).first.click()
        session.settle_and_wait_fonts(200)


def _verify_unschedulable_quota_empty_state(session: Session, timeout_ms: int) -> None:
    """Filters with an unschedulable amount to verify the empty queue state, then resets to 1."""
    amount_input = session.page.locator(RESOURCE_AMOUNT_INPUT_SEL).first
    amount_input.fill("999999999")
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(QUEUE_LIST_EMPTY_SEL, timeout=timeout_ms)
    amount_input.fill("1")
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(QUEUE_CARD_SEL, timeout=timeout_ms)


def assert_smart_resource_finder_and_predictability(
    session: Session, timeout_ms: int = 15000
) -> str:
    """Exercises Smart Resource Finder controls, predictability metrics, and timeframe toggles."""
    print("\n[ROD UI] Exercising Smart Resource Finder & Predictability column...")
    _assert_initial_predictability_empty_state(session, timeout_ms)
    resource_name = _select_primary_resource_and_amount(session, timeout_ms)
    _verify_velocity_timeframe_pills(session, timeout_ms)
    _verify_multi_resource_row_add_and_remove(session)
    _verify_unschedulable_quota_empty_state(session, timeout_ms)
    print(f"  [✓] Verified Smart Resource Finder & Predictability for resource '{resource_name}'")
    return resource_name


def assert_subnav_tabs_switching(session: Session, timeout_ms: int = 15000) -> None:
    """Switches between ROD subnavigation tabs and verifies view transitions."""
    print("\n[ROD UI] Switching subnavigation tabs...")
    session.page.locator(AVAILABILITY_TAB_SEL).first.click()
    session.page.wait_for_selector(WIP_BANNER_SEL, timeout=timeout_ms)
    session.take_screenshot("ui_rod_step4_resource_availability.png")
    session.page.locator(SMART_FINDER_TAB_SEL).first.click()
    session.page.wait_for_selector(SMART_FINDER_ROOT_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()
    print("  [✓] Verified ROD subnavigation tab switching")


def verify_resource_dashboard_in_session(
    session: Session, expected_queue: str | None = None, timeout_ms: int = 30000
) -> dict[str, Any]:
    """Executes the full Resource Observability Dashboard UI E2E suite in an active session."""
    step1 = "ui_rod_step1_landing.png"
    session.navigate_to_route("/resources", DASHBOARD_ROOT_SEL, step1, timeout_ms)
    queues = assert_queue_cards(session, expected_queue=expected_queue, timeout_ms=timeout_ms)
    selected_q = assert_queue_selection_and_jobs_pane(session, queues)
    session.take_screenshot("ui_rod_step2_queue_selection.png")
    selected_res = assert_smart_resource_finder_and_predictability(session)
    session.take_screenshot("ui_rod_step3_smart_resource_finder.png")
    assert_subnav_tabs_switching(session)
    session.assert_no_runtime_errors("resource_dashboard", (":batchGetSecondaryData",))
    return {"queues": queues, "selected_queue": selected_q, "selected_resource": selected_res}


def verify_resource_dashboard_ui(
    ui_base_url: str,
    auth_token: str | None = None,
    headless: bool = True,
    screenshots_dir: str | None = None,
    expected_queue: str | None = None,
) -> dict[str, Any]:
    """Launches a Playwright session and verifies the Resource Observability Dashboard UI."""
    with Session(ui_base_url, auth_token, screenshots_dir, headless) as session:
        res = verify_resource_dashboard_in_session(session, expected_queue=expected_queue)
        rows = [
            ("UI Base URL", ui_base_url),
            ("Verified Queues", ", ".join(res["queues"])),
            ("Selected Queue Side Pane", res["selected_queue"]),
            ("Smart Finder Resource", res["selected_resource"]),
            ("Diagnostic Screenshots", session.screenshots_dir),
        ]
        verification_report.print_success_summary("ROD UI E2E PASSED SUCCESSFULLY!", rows)
        return res


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone ROD UI E2E verification."""
    verifier_cli.validate_argv(argv)
    get = verifier_cli.get_flag
    ui_url = get(FLAGS, "ui_base_url") or endpoint_resolution.resolve_ui_base_url()
    args = (get(FLAGS, "auth_token"), get(FLAGS, "headless", True), get(FLAGS, "screenshots_dir"))
    with ess_session.handle_verification_errors():
        verify_resource_dashboard_ui(ui_url, *args, expected_queue=get(FLAGS, "expected_queue"))


if __name__ == "__main__":
    verifier_cli.run_main(main)
