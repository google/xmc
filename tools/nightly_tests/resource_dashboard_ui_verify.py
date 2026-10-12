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

import re
from collections.abc import Sequence
from typing import Any, TypeAlias

from absl import flags

from tools.nightly_tests import ui_css_selectors as sel, ui_entities_validator
from tools.nightly_tests.verification import (
    endpoint_resolution as ep,
    ess_session as ess,
    verification_report as rep,
    verifier_cli as cli,
)

FLAGS = flags.FLAGS
flags.DEFINE_string("ui_base_url", None, "Base URL of the XMC Web UI", allow_override=True)
flags.DEFINE_string("auth_token", None, "Optional Bearer/IAP auth token", allow_override=True)
flags.DEFINE_bool("headless", True, "Run browser in headless mode", allow_override=True)
flags.DEFINE_string("screenshots_dir", None, "Diagnostic screenshots dir", allow_override=True)

EXPECTED_CLUSTER_QUEUES = ("cpu-cluster-queue", "gpu-cluster-queue", "tpu-cluster-queue")
EXPECTED_FIRST_CARD_FLAVOR: str = "DefaultFlavor / CPU"
EXPECTED_WORK_UNITS: tuple[str, ...] = ("1", "2", "3")
EXPECTED_RUNNING_POS: str = "N/A - running"
VALID_RESOURCE_AMOUNT: str = "1"
UNSCHEDULABLE_RESOURCE_AMOUNT: str = "999999999"
VELOCITY_TIMEFRAMES: tuple[str, ...] = ("1h", "24h", "7d")

Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession
UIError: TypeAlias = ui_entities_validator.UIVerificationError


def _resolve_cluster_queue(expected_queue: str | None) -> str:
    """Resolves a submit queue or cluster queue name to its rendered cluster queue title."""
    cq = expected_queue or EXPECTED_CLUSTER_QUEUES[0]
    return cq if cq.endswith("-cluster-queue") else cq.replace("-queue", "-cluster-queue")


def _wait_for_queue_cards_with_retry(session: Session, timeout_ms: int) -> None:
    """Waits for queue cards to render, reloading once if initial load hit a cold-start 500."""
    try:
        session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=5000)
    except Exception:
        session.http_errors.clear()
        session.page.reload(wait_until="networkidle", timeout=timeout_ms)
        session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=timeout_ms)
    session.settle_and_wait_fonts()


def _extract_and_verify_queue_titles(session: Session, expected_q: str | None) -> list[str]:
    """Extracts rendered queue card titles and verifies expected cluster queues are present."""
    raw = session.page.locator(sel.QUEUE_TITLE_SEL).all_inner_texts()
    titles = [t.strip() for t in raw if t.strip()]
    if missing := [q for q in EXPECTED_CLUSTER_QUEUES if q not in titles]:
        raise UIError(f"Missing expected queues {missing} in rendered queues: {titles}")
    if expected_q and not ({expected_q, _resolve_cluster_queue(expected_q)} & set(titles)):
        raise UIError(f"Missing '{expected_q}' in {titles}")
    return titles


def assert_queue_cards(session: Session, expected_queue: str | None = None) -> list[str]:
    """Asserts that cluster queue cards and quota flavors render on /resources."""
    _wait_for_queue_cards_with_retry(session, 30000)
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


def _collect_xid_work_unit_positions(session: Session, xid: str) -> dict[str, str]:
    """Returns a mapping of workUnitId -> position text for rows matching Experiment #<xid>."""
    rows = session.page.locator(sel.JOBS_TABLE_ROW_SEL).filter(has_text=f"Experiment #{xid}")
    positions: dict[str, str] = {}
    for i in range(rows.count()):
        wid = (rows.nth(i).locator(sel.JOB_WORKUNIT_CELL_SEL).first.text_content() or "").strip()
        pos = (rows.nth(i).locator(sel.JOB_POSITION_CELL_SEL).first.text_content() or "").strip()
        if wid:
            positions[wid] = pos
    return positions


def _verify_work_units_in_jobs_table(
    session: Session, target_cq: str, alt_cq: str, xid: str | None
) -> None:
    """Polls the Jobs in the Queue table until all 3 work units appear with valid positions."""
    if not xid or not str(xid).isdigit():
        return
    for attempt in range(12):
        pos = _collect_xid_work_unit_positions(session, str(xid))
        vals = [pos.get(w, "") for w in EXPECTED_WORK_UNITS]
        if all(v == EXPECTED_RUNNING_POS or v.isdigit() for v in vals):
            print(f"  [✓] Verified '{target_cq}' Jobs table has XID {xid} work units: {pos}")
            return
        if attempt < 11:
            _select_queue_card_by_name(session, alt_cq)
            session.settle_and_wait_fonts(1500)
            _select_queue_card_by_name(session, target_cq)
    raise UIError(f"Missing XID {xid} work units {EXPECTED_WORK_UNITS} in '{target_cq}': {pos}")


def assert_queue_selection_and_jobs_pane(
    session: Session, queues: Sequence[str], expected_q: str | None = None, xid: str | None = None
) -> str:
    """Clicks queue cards and verifies the Jobs in the Queue side pane and active work units."""
    target_cq = _resolve_cluster_queue(expected_q)
    alt_cq = next((q for q in queues if q != target_cq), target_cq)
    _select_queue_card_by_name(session, alt_cq)
    actual_name = _select_queue_card_by_name(session, target_cq)
    _verify_work_units_in_jobs_table(session, target_cq, alt_cq, xid)
    print(f"  [✓] Verified Jobs in the Queue side pane synchronized to '{actual_name}'")
    return actual_name


def _select_primary_resource_and_amount(session: Session, timeout_ms: int) -> str:
    """Selects CPU in Smart Resource Finder and sets the valid amount."""
    session.page.locator(sel.RESOURCE_SELECT_SEL).first.click()
    session.page.wait_for_selector(sel.RESOURCE_OPTION_SEL, timeout=timeout_ms)
    options = session.page.locator(sel.RESOURCE_OPTION_SEL)
    if options.count() < 2:
        raise UIError("Missing selectable resource mat-option.")
    cpu_opt = options.filter(has_text="CPU").first
    target_option = cpu_opt if cpu_opt.count() > 0 else options.nth(1)
    resource_name = (target_option.text_content() or "").strip()
    target_option.click()
    session.page.locator(sel.RESOURCE_AMOUNT_INPUT_SEL).first.fill(VALID_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.PREDICTABILITY_CONTENT_SEL, timeout=timeout_ms)
    return resource_name


def _parse_velocity_rate(metric_text: str) -> float:
    """Extracts the numeric velocity rate from a display string such as '0.13 jobs/hr'."""
    match = re.search(r"([0-9]+(?:\.[0-9]+)?)", metric_text)
    return float(match.group(1)) if match else 0.0


def _verify_expected_queue_positive_7d_velocity(
    session: Session, expected_queue: str | None, timeout_ms: int
) -> None:
    """Verifies that the expected queue card displays positive velocity under the 7d period."""
    target_cq = _resolve_cluster_queue(expected_queue)
    card = session.page.locator(sel.QUEUE_CARD_SEL).filter(has_text=target_cq).first
    grid = card.locator(sel.PREDICTABILITY_STATS_GRID_SEL).first
    grid.wait_for(state="visible", timeout=timeout_ms)
    vals = [t.strip() for t in card.locator(sel.PREDICTABILITY_METRIC_VAL_SEL).all_inner_texts()]
    if not any(_parse_velocity_rate(v) > 0 for v in vals[:2]):
        raise UIError(f"Expected positive 7d velocity on '{target_cq}', got metrics: {vals}")
    print(f"  [✓] Verified positive 7d velocity on '{target_cq}': {vals[:2]}")


def _verify_unschedulable_quota_empty_state(session: Session, timeout_ms: int) -> None:
    """Filters with an unschedulable amount to verify the empty queue state, then resets."""
    amount_input = session.page.locator(sel.RESOURCE_AMOUNT_INPUT_SEL).first
    amount_input.fill(UNSCHEDULABLE_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.QUEUE_LIST_EMPTY_SEL, timeout=timeout_ms)
    amount_input.fill(VALID_RESOURCE_AMOUNT)
    session.settle_and_wait_fonts()
    session.page.wait_for_selector(sel.QUEUE_CARD_SEL, timeout=timeout_ms)


def assert_smart_resource_finder_and_predictability(
    session: Session, expected_queue: str | None = None
) -> str:
    """Exercises Smart Resource Finder controls, predictability metrics, and timeframe toggles."""
    session.page.wait_for_selector(sel.PREDICTABILITY_EMPTY_SEL, timeout=15000)
    if session.page.locator(sel.PREDICTABILITY_EMPTY_SEL).count() == 0:
        raise UIError(f"Missing {sel.PREDICTABILITY_EMPTY_SEL}.")
    resource_name = _select_primary_resource_and_amount(session, 15000)
    for tf in VELOCITY_TIMEFRAMES:
        session.page.locator(sel.VELOCITY_PILL_SEL_TMPL.format(tf=tf)).first.click()
        session.page.wait_for_selector(sel.ACTIVE_PILL_SEL_TMPL.format(tf=tf), timeout=15000)
    _verify_expected_queue_positive_7d_velocity(session, expected_queue, 15000)
    _verify_unschedulable_quota_empty_state(session, 15000)
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
    rep.print_success_summary("ROD UI E2E PASSED SUCCESSFULLY!", rows)
    return {"queues": queues, "selected_queue": selected_q, "selected_resource": selected_res}


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for standalone ROD UI E2E verification."""
    cli.validate_argv(argv)
    get = cli.get_flag
    ui_url = get(FLAGS, "ui_base_url") or ep.resolve_ui_base_url()
    args = (ui_url, get(FLAGS, "auth_token"), get(FLAGS, "screenshots_dir"), get(FLAGS, "headless"))
    with ess.handle_verification_errors(), Session(*args) as session:
        verify_resource_dashboard_ui(session, get(FLAGS, "expected_queue"), get(FLAGS, "xid"))


if __name__ == "__main__":
    cli.run_main(main)
