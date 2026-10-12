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

from tools.nightly_tests import ui_css_selectors as sel, ui_entities_validator
from tools.nightly_tests.verification import (
    endpoint_resolution as ep,
    ess_session,
    verification_report,
    verifier_cli,
)

FLAGS = flags.FLAGS
flags.DEFINE_string("ui_base_url", None, "Base URL of the XMC Web UI", allow_override=True)
flags.DEFINE_string("auth_token", None, "Optional Bearer/IAP auth token", allow_override=True)
flags.DEFINE_bool("headless", True, "Run browser in headless mode", allow_override=True)
flags.DEFINE_string("screenshots_dir", None, "Diagnostic screenshots dir", allow_override=True)

EXPECTED_CLUSTER_QUEUES = ("cpu-cluster-queue", "gpu-cluster-queue", "tpu-cluster-queue")
EXPECTED_FIRST_CARD_FLAVOR: str = "DefaultFlavor / CPU"
EXPECTED_READY_STATUS: str = "Ready"
EXPECTED_COPIED_LABEL: str = "Copied"
EXPECTED_PRIMARY_RESOURCE: str = "CPU"
VALID_RESOURCE_AMOUNT: str = "1"
UNSCHEDULABLE_RESOURCE_AMOUNT: str = "999999999"
VELOCITY_TIMEFRAMES: tuple[str, ...] = ("1h", "24h", "7d")
ACTIVE_TOGGLE_CLASS: str = "active-toggle"
MIN_MULTI_RESOURCE_ROWS: int = 2

Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError


def _wait_for_queue_cards_with_retry(session: Session, timeout_ms: int) -> None:
    """Waits for queue cards to render, reloading once if initial load hit a cold-start 500."""
    try:
        session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=5000)
    except Exception:
        session.http_errors.clear()
        session.page.reload(wait_until="networkidle", timeout=timeout_ms)
        session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()


def _extract_and_verify_queue_titles(session: Session, expected_queue: str | None) -> list[str]:
    """Extracts rendered queue card titles and verifies expected cluster queues are present."""
    raw_titles = session.page.locator(sel.QUEUE_TITLE_SEL).all_inner_texts()
    titles = [t.strip() for t in raw_titles if t and t.strip()]
    if missing := [q for q in EXPECTED_CLUSTER_QUEUES if q not in titles]:
        raise UIError(f"Missing expected queues {missing} in rendered queues: {titles}")
    cq = expected_queue.replace("-queue", "-cluster-queue") if expected_queue else ""
    if expected_queue and not ({expected_queue, cq} & set(titles)):
        raise UIError(f"Missing '{expected_queue}' in {titles}")
    return titles


def _verify_first_card_capacity_and_status(first_card: Any) -> tuple[str, str]:
    """Verifies that the first queue card renders the expected quota flavor and Ready status."""
    flavor = (first_card.locator(sel.FLAVOR_NAME_SEL).first.text_content() or "").strip()
    badge = (first_card.locator(sel.STATUS_BADGE_SEL).first.text_content() or "").strip()
    if flavor != EXPECTED_FIRST_CARD_FLAVOR or EXPECTED_READY_STATUS not in badge:
        raise UIError(f"Unexpected flavor/status: {flavor} / {badge}")
    return flavor, badge


def _verify_copy_flag_action(session: Session, first_card: Any, timeout_ms: int) -> None:
    """Clicks the Copy flag button on the first card and verifies the 'Copied' state transition."""
    copy_btn = first_card.locator(sel.COPY_FLAG_BTN_SEL).first
    copy_btn.click()
    session.page.wait_for_selector(sel.COPIED_FLAG_BTN_SEL, timeout=timeout_ms)
    btn_text = (copy_btn.text_content() or "").strip()
    if EXPECTED_COPIED_LABEL not in btn_text:
        raise UIError(f"Expected '{EXPECTED_COPIED_LABEL}', got '{btn_text}'.")


def assert_queue_cards(session: Session, expected_queue: str | None = None) -> list[str]:
    """Asserts that cluster queue cards, quota flavors, and copy-flag actions render on /resources."""
    print("\n[ROD UI] Asserting cluster queue cards and capacity metadata...")
    _wait_for_queue_cards_with_retry(session, 30000)
    titles = _extract_and_verify_queue_titles(session, expected_queue)
    first_card = session.page.locator(sel.QUEUE_CARD_SEL).first
    flavor, badge = _verify_first_card_capacity_and_status(first_card)
    _verify_copy_flag_action(session, first_card, 30000)
    print(f"  [✓] Verified {len(titles)} queue card(s): {titles} (flavor={flavor}, status={badge})")
    return titles


def assert_queue_selection_and_jobs_pane(session: Session, queues: Sequence[str]) -> str:
    """Clicks a queue card and verifies that the Jobs in the Queue side pane updates."""
    target_idx = 1 if len(queues) > 1 else 0
    expected_name = queues[target_idx]
    print(f"\n[ROD UI] Selecting queue card #{target_idx + 1} ('{expected_name}')...")
    session.page.locator(sel.QUEUE_CARD_SEL).nth(target_idx).click()
    session.settle_and_wait_fonts()
    header_loc = session.page.locator(sel.JOBS_PANE_QUEUE_NAME_SEL).first
    header_loc.wait_for(state="visible", timeout=15000)
    actual_name = (header_loc.text_content() or "").strip()
    if actual_name != expected_name:
        raise UIError(f"Expected '{expected_name}', got '{actual_name}'.")
    session.page.wait_for_selector(sel.JOBS_CONTENT_SEL, timeout=15000)
    print(f"  [✓] Verified Jobs in the Queue side pane synchronized to '{actual_name}'")
    return actual_name


def _assert_initial_predictability_empty_state(session: Session, timeout_ms: int) -> None:
    """Verifies that the predictability column starts in its empty prompt state."""
    session.page.wait_for_selector(sel.PREDICTABILITY_EMPTY_SEL, timeout=timeout_ms)
    if session.page.locator(sel.PREDICTABILITY_EMPTY_SEL).count() == 0:
        raise UIError(f"Missing {sel.PREDICTABILITY_EMPTY_SEL}.")


def _select_primary_resource_and_amount(session: Session, timeout_ms: int) -> str:
    """Selects the primary resource type in Smart Resource Finder and sets the valid amount."""
    session.page.locator(sel.RESOURCE_SELECT_SEL).first.click()
    session.page.wait_for_selector(sel.RESOURCE_OPTION_SEL, timeout=timeout_ms)
    options = session.page.locator(sel.RESOURCE_OPTION_SEL)
    if options.count() < 2:
        raise UIError("Missing selectable resource mat-option.")
    target_option = options.nth(1)
    resource_name = (target_option.text_content() or "").strip()
    if resource_name != EXPECTED_PRIMARY_RESOURCE:
        raise UIError(f"Expected resource '{EXPECTED_PRIMARY_RESOURCE}', got '{resource_name}'.")
    target_option.click()
    session.page.locator(sel.RESOURCE_AMOUNT_INPUT_SEL).first.fill(VALID_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.PREDICTABILITY_CONTENT_SEL, timeout=timeout_ms)
    return resource_name


def _verify_velocity_timeframe_pills(session: Session, timeout_ms: int) -> None:
    """Clicks each velocity timeframe toggle pill and verifies it becomes active."""
    for tf in VELOCITY_TIMEFRAMES:
        pill = session.page.locator(sel.VELOCITY_PILL_SEL_TMPL.format(tf=tf)).first
        pill.click()
        session.page.wait_for_selector(sel.ACTIVE_PILL_SEL_TMPL.format(tf=tf), timeout=timeout_ms)
        if ACTIVE_TOGGLE_CLASS not in (pill.get_attribute("class") or ""):
            raise UIError(f"Pill '{tf}' missing '{ACTIVE_TOGGLE_CLASS}'.")


def _verify_multi_resource_row_add_and_remove(session: Session) -> None:
    """Adds a secondary resource filter row and removes it."""
    add_btn = session.page.locator(sel.ADD_RESOURCE_BTN_SEL).first
    if add_btn.count() > 0 and add_btn.is_enabled():
        add_btn.click()
        session.settle_and_wait_fonts(200)
        row_count = session.page.locator(sel.RESOURCE_QUERY_ROW_SEL).count()
        if row_count < MIN_MULTI_RESOURCE_ROWS:
            raise UIError(f"Expected >= {MIN_MULTI_RESOURCE_ROWS} rows, got {row_count}.")
        session.page.locator(sel.REMOVE_RESOURCE_BTN_SEL).first.click()
        session.settle_and_wait_fonts(200)


def _verify_unschedulable_quota_empty_state(session: Session, timeout_ms: int) -> None:
    """Filters with an unschedulable amount to verify the empty queue state, then resets."""
    amount_input = session.page.locator(sel.RESOURCE_AMOUNT_INPUT_SEL).first
    amount_input.fill(UNSCHEDULABLE_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.QUEUE_LIST_EMPTY_SEL, timeout=timeout_ms)
    amount_input.fill(VALID_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=timeout_ms)


def assert_smart_resource_finder_and_predictability(session: Session) -> str:
    """Exercises Smart Resource Finder controls, predictability metrics, and timeframe toggles."""
    print("\n[ROD UI] Exercising Smart Resource Finder & Predictability column...")
    _assert_initial_predictability_empty_state(session, 15000)
    resource_name = _select_primary_resource_and_amount(session, 15000)
    _verify_velocity_timeframe_pills(session, 15000)
    _verify_multi_resource_row_add_and_remove(session)
    _verify_unschedulable_quota_empty_state(session, 15000)
    print(f"  [✓] Verified Smart Resource Finder & Predictability for resource '{resource_name}'")
    return resource_name


def assert_subnav_tabs_switching(session: Session, timeout_ms: int = 15000) -> None:
    """Switches between ROD subnavigation tabs and verifies view transitions."""
    print("\n[ROD UI] Switching subnavigation tabs...")
    session.page.locator(sel.AVAILABILITY_TAB_SEL).first.click()
    session.page.wait_for_selector(sel.WIP_BANNER_SEL, timeout=timeout_ms)
    session.take_screenshot("ui_rod_step4_resource_availability.png")
    session.page.locator(sel.SMART_FINDER_TAB_SEL).first.click()
    session.page.wait_for_selector(sel.SMART_FINDER_ROOT_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()
    print("  [✓] Verified ROD subnavigation tab switching")


def verify_resource_dashboard_ui(session: Session, expected_q: str | None = None) -> dict[str, Any]:
    """Executes the Resource Observability Dashboard UI E2E verification suite."""
    session.navigate_to_route("/resources", sel.DASHBOARD_ROOT_SEL, "ui_rod_step1_landing.png")
    queues = assert_queue_cards(session, expected_queue=expected_q)
    selected_q = assert_queue_selection_and_jobs_pane(session, queues)
    session.take_screenshot("ui_rod_step2_queue_selection.png")
    selected_res = assert_smart_resource_finder_and_predictability(session)
    session.take_screenshot("ui_rod_step3_smart_resource_finder.png")
    assert_subnav_tabs_switching(session)
    session.assert_no_runtime_errors("resource_dashboard", (":batchGetSecondaryData",))
    rows = [
        ("UI Base URL", session.ui_base_url),
        ("Verified Queues", ", ".join(queues)),
        ("Selected Queue", selected_q),
        ("Smart Finder Resource", selected_res),
    ]
    verification_report.print_success_summary("ROD UI E2E PASSED SUCCESSFULLY!", rows)
    return {"queues": queues, "selected_queue": selected_q, "selected_resource": selected_res}


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone ROD UI E2E verification."""
    verifier_cli.validate_argv(argv)
    get = verifier_cli.get_flag
    ui_url = get(FLAGS, "ui_base_url") or ep.resolve_ui_base_url()
    args = (ui_url, get(FLAGS, "auth_token"), get(FLAGS, "screenshots_dir"), get(FLAGS, "headless"))
    with ess_session.handle_verification_errors(), Session(*args) as session:
        verify_resource_dashboard_ui(session, expected_q=get(FLAGS, "expected_queue"))


if __name__ == "__main__":
    verifier_cli.run_main(main)
