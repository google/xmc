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
from typing import Any

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

VELOCITY_TIMEFRAMES: tuple[str, ...] = ("1h", "6h", "24h", "7d")


def assert_queue_cards(
    session: ui_entities_validator.XMCUIBrowserSession,
    expected_queue: str | None = None,
    timeout_ms: int = 30000,
) -> list[str]:
    """Asserts that cluster queue cards, quota flavors, and copy-flag actions render on /resources."""
    print("\n[ROD UI] Asserting cluster queue cards and capacity metadata...")
    try:
        session.page.wait_for_selector("xmc-rod-queue-card .queue-card", timeout=5000)
    except Exception:
        session.http_errors.clear()
        session.page.reload(wait_until="networkidle", timeout=timeout_ms)
        session.page.wait_for_selector("xmc-rod-queue-card .queue-card", timeout=timeout_ms)
    session.settle_and_wait_fonts()
    raw_titles = session.page.locator("xmc-rod-queue-card .queue-title").all_inner_texts()
    titles = [t.strip() for t in raw_titles if t and t.strip()]
    if not titles:
        raise ui_entities_validator.UIVerificationError("No cluster queue cards on /resources.")
    cq = expected_queue.replace("-queue", "-cluster-queue") if expected_queue else ""
    if expected_queue and not ({expected_queue, cq} & set(titles)):
        raise ui_entities_validator.UIVerificationError(
            f"Expected queue '{expected_queue}' not found in rendered queues: {titles}"
        )
    first_card = session.page.locator("xmc-rod-queue-card .queue-card").first
    flavor_loc = first_card.locator(".capacity-box .flavor-main-name").first
    flavor = (flavor_loc.text_content() or "").strip()
    badge = (first_card.locator(".status-badge").first.text_content() or "").strip()
    if not flavor or not badge:
        raise ui_entities_validator.UIVerificationError(
            f"Queue card missing flavor ('{flavor}') or status badge ('{badge}')."
        )
    copy_btn = first_card.locator("button.copy-flag-btn").first
    copy_btn.click()
    session.page.wait_for_selector(
        'xmc-rod-queue-card button.copy-flag-btn:has-text("Copied")', timeout=timeout_ms
    )
    btn_text = (copy_btn.text_content() or "").strip()
    if "Copied" not in btn_text:
        raise ui_entities_validator.UIVerificationError(
            f"Expected copy-flag button text to transition to 'Copied', got '{btn_text}'."
        )
    print(f"  [✓] Verified {len(titles)} queue card(s): {titles} (flavor={flavor}, status={badge})")
    return titles


def assert_queue_selection_and_jobs_pane(
    session: ui_entities_validator.XMCUIBrowserSession,
    queue_titles: Sequence[str],
    timeout_ms: int = 15000,
) -> str:
    """Clicks a queue card and verifies that the Jobs in the Queue side pane updates."""
    target_idx = 1 if len(queue_titles) > 1 else 0
    expected_name = queue_titles[target_idx]
    print(f"\n[ROD UI] Selecting queue card #{target_idx + 1} ('{expected_name}')...")
    session.page.locator("xmc-rod-queue-card .queue-card").nth(target_idx).click()
    session.settle_and_wait_fonts()
    header_loc = session.page.locator("xmc-queue-jobs-table .target-queue-name").first
    header_loc.wait_for(state="visible", timeout=timeout_ms)
    actual_name = (header_loc.text_content() or "").strip()
    if actual_name != expected_name:
        raise ui_entities_validator.UIVerificationError(
            f"Expected side pane queue header '{expected_name}', but found '{actual_name}'."
        )
    jobs_sel = "xmc-queue-jobs-table table.jobs-mat-table, xmc-queue-jobs-table .jobs-empty-state"
    session.page.wait_for_selector(jobs_sel, timeout=timeout_ms)
    print(f"  [✓] Verified Jobs in the Queue side pane synchronized to '{actual_name}'")
    return actual_name


def _exercise_resource_rows_and_unschedulable_filter(
    session: ui_entities_validator.XMCUIBrowserSession, timeout_ms: int = 15000
) -> None:
    """Exercises multi-resource row addition/removal and unschedulable quota filtering."""
    add_btn = session.page.locator("xmc-smart-resource-finder button.add-resource-btn").first
    if add_btn.count() > 0 and add_btn.is_enabled():
        add_btn.click()
        session.settle_and_wait_fonts(200)
        row_count = session.page.locator("xmc-smart-resource-finder .query-row").count()
        if row_count < 2:
            raise ui_entities_validator.UIVerificationError(
                f"Expected >= 2 resource query rows after clicking add, found {row_count}."
            )
        session.page.locator("xmc-smart-resource-finder button.remove-resource-btn").first.click()
        session.settle_and_wait_fonts(200)
    amount_input = session.page.locator("xmc-smart-resource-finder input.query-amount-input").first
    amount_input.fill("999999999")
    session.settle_and_wait_fonts()
    session.page.wait_for_selector("xmc-rod-queue-list .empty-state", timeout=timeout_ms)
    amount_input.fill("1")
    session.settle_and_wait_fonts()
    session.page.wait_for_selector("xmc-rod-queue-card .queue-card", timeout=timeout_ms)


def assert_smart_resource_finder_and_predictability(
    session: ui_entities_validator.XMCUIBrowserSession, timeout_ms: int = 15000
) -> str:
    """Exercises Smart Resource Finder controls, predictability metrics, and timeframe toggles."""
    print("\n[ROD UI] Exercising Smart Resource Finder & Predictability column...")
    session.page.wait_for_selector(".predictability-empty-state", timeout=timeout_ms)
    if session.page.locator(".predictability-empty-state").count() == 0:
        raise ui_entities_validator.UIVerificationError(
            "Missing initial .predictability-empty-state."
        )
    session.page.locator("xmc-smart-resource-finder mat-select.query-select-box").first.click()
    session.page.wait_for_selector("mat-option", timeout=timeout_ms)
    options = session.page.locator("mat-option")
    if options.count() < 2:
        raise ui_entities_validator.UIVerificationError("Missing selectable resource mat-option.")
    target_option = options.nth(1)
    resource_name = (target_option.text_content() or "").strip()
    target_option.click()
    session.page.locator("xmc-smart-resource-finder input.query-amount-input").first.fill("1")
    session.settle_and_wait_fonts()
    pred_sel = ".predictability-stats-grid, .predictability-spinner-container"
    session.page.wait_for_selector(pred_sel, timeout=timeout_ms)
    for tf in VELOCITY_TIMEFRAMES:
        pill = session.page.locator(f'xmc-rod-queue-list button.toggle-pill:has-text("{tf}")').first
        pill.click()
        active_sel = f'xmc-rod-queue-list button.toggle-pill.active-toggle:has-text("{tf}")'
        session.page.wait_for_selector(active_sel, timeout=timeout_ms)
        if "active-toggle" not in (pill.get_attribute("class") or ""):
            raise ui_entities_validator.UIVerificationError(f"Pill '{tf}' missing 'active-toggle'.")
    _exercise_resource_rows_and_unschedulable_filter(session, timeout_ms=timeout_ms)
    print(f"  [✓] Verified Smart Resource Finder & Predictability for resource '{resource_name}'")
    return resource_name


def assert_subnav_tabs_switching(
    session: ui_entities_validator.XMCUIBrowserSession, timeout_ms: int = 15000
) -> None:
    """Switches between ROD subnavigation tabs and verifies view transitions."""
    print("\n[ROD UI] Switching subnavigation tabs...")
    avail_sel = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Resource availability")'
    session.page.locator(avail_sel).first.click()
    session.page.wait_for_selector("xmc-wip-banner", timeout=timeout_ms)
    session.take_screenshot("ui_rod_step4_resource_availability.png")
    finder_sel = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Smart resource finder")'
    session.page.locator(finder_sel).first.click()
    session.page.wait_for_selector("xmc-smart-resource-finder", timeout=timeout_ms)
    session.settle_and_wait_fonts()
    print("  [✓] Verified ROD subnavigation tab switching")


def verify_resource_dashboard_in_session(
    session: ui_entities_validator.XMCUIBrowserSession,
    expected_queue: str | None = None,
    timeout_ms: int = 30000,
) -> dict[str, Any]:
    """Executes the full Resource Observability Dashboard UI E2E suite in an active session."""
    session.navigate_to_route(
        "/resources", "xmc-resource-dashboard", "ui_rod_step1_landing.png", timeout_ms=timeout_ms
    )
    queues = assert_queue_cards(session, expected_queue=expected_queue, timeout_ms=timeout_ms)
    selected_queue = assert_queue_selection_and_jobs_pane(session, queues)
    session.take_screenshot("ui_rod_step2_queue_selection.png")
    selected_resource = assert_smart_resource_finder_and_predictability(session)
    session.take_screenshot("ui_rod_step3_smart_resource_finder.png")
    assert_subnav_tabs_switching(session)
    session.assert_no_runtime_errors(
        "resource_dashboard", ignored_http_substrings=(":batchGetSecondaryData",)
    )
    return {
        "queues": queues,
        "selected_queue": selected_queue,
        "selected_resource": selected_resource,
    }


def verify_resource_dashboard_ui(
    ui_base_url: str,
    auth_token: str | None = None,
    headless: bool = True,
    screenshots_dir: str | None = None,
    expected_queue: str | None = None,
) -> dict[str, Any]:
    """Launches a Playwright session and verifies the Resource Observability Dashboard UI."""
    with ui_entities_validator.XMCUIBrowserSession(
        ui_base_url=ui_base_url,
        auth_token=auth_token,
        headless=headless,
        screenshots_dir=screenshots_dir,
    ) as session:
        summary = verify_resource_dashboard_in_session(session, expected_queue=expected_queue)
        verification_report.print_success_summary(
            "RESOURCE OBSERVABILITY DASHBOARD (ROD) UI E2E PASSED SUCCESSFULLY!",
            [
                ("UI Base URL", ui_base_url),
                ("Verified Queues", ", ".join(summary["queues"])),
                ("Selected Queue Side Pane", summary["selected_queue"]),
                ("Smart Finder Resource", summary["selected_resource"]),
                ("Diagnostic Screenshots", session.screenshots_dir),
            ],
        )
        return summary


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone ROD UI E2E verification."""
    verifier_cli.validate_argv(argv)
    with ess_session.handle_verification_errors():
        verify_resource_dashboard_ui(
            ui_base_url=verifier_cli.get_flag(FLAGS, "ui_base_url")
            or endpoint_resolution.resolve_ui_base_url(),
            auth_token=verifier_cli.get_flag(FLAGS, "auth_token"),
            headless=verifier_cli.get_flag(FLAGS, "headless", True),
            screenshots_dir=verifier_cli.get_flag(FLAGS, "screenshots_dir"),
            expected_queue=verifier_cli.get_flag(FLAGS, "expected_queue"),
        )


if __name__ == "__main__":
    verifier_cli.run_main(main)
