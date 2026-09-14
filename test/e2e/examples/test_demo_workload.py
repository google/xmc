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

"""Tier 2 E2E Integration Test for Demo Workload (Simple Job).

Validates end-to-end execution of the canonical XMC demonstration workload
(examples/simple_job/launch.py) against a local KinD cluster, performing backend
entity assertions via tools.nightly_tests.xmc_entities_validator and visual
verification in the Angular UI via Playwright.
"""

from __future__ import annotations

import logging
import sys
from pathlib import Path
from typing import Any

from playwright.sync_api import Page, expect, sync_playwright

from test.e2e.examples.example_runner import (
    ExampleLaunchResult,
    execute_example_launch,
)
from test.e2e.lib.config import (
    DEFAULT_ADMIN_PASSWORD,
    DEFAULT_ADMIN_USER,
    DEFAULT_BASE_URL,
    DEFAULT_DEX_URL,
)
from test.e2e.lib.dex_authenticator import DexAuthenticator
from test.e2e.lib.ui_diagnostics import (
    get_screenshots_dir,
    save_screenshot,
    setup_logging,
)
from tools.nightly_tests import xmc_entities_validator

logger = logging.getLogger(__name__)

DEFAULT_TIMEOUT_MS = 30_000
BASE_URL = DEFAULT_BASE_URL
DEX_URL = DEFAULT_DEX_URL
ADMIN_USER = DEFAULT_ADMIN_USER
ADMIN_PASSWORD = DEFAULT_ADMIN_PASSWORD

EXPECTED_WORK_UNITS = 3
EXPECTED_QUEUE = "cpu-queue"
EXPECTED_TITLE_PATTERN = "Demo Workload"
EXPECTED_WORKER_NAMES = (
    "demo-worker-1",
    "demo-worker-2",
    "demo-worker-3",
)


def _launch_demo_workload(
    page: Page,
    auth: DexAuthenticator,
    screenshots_dir: Path,
) -> ExampleLaunchResult:
    """Authenticates via Dex and launches the demo workload example via subprocess."""
    logger.info("=== [Level 1] Authenticating Admin Persona via Dex OIDC ===")
    auth.authenticate(page, ADMIN_USER, ADMIN_PASSWORD, screenshots_dir)

    logger.info("=== Executing Demo Workload Launch Script (examples/simple_job/launch.py) ===")
    launch_result = execute_example_launch(
        launch_script="examples/simple_job/launch.py",
        user_email=ADMIN_USER,
        args=[
            f"--queue={EXPECTED_QUEUE}",
            f"--num_work_units={EXPECTED_WORK_UNITS}",
            "--duration=30",
            "--gpus=0",
            f"--author={ADMIN_USER}",
        ],
    )

    assert launch_result.xid, "Experiment XID must not be empty in launch output"
    logger.info("✓ Successfully launched demo workload with XID: %s", launch_result.xid)
    return launch_result


def _verify_demo_workload_backend(xid: str) -> tuple[Any, list[dict[str, Any]]]:
    """Verifies experiment metadata and WorkUnits using shared xmc_entities_validator."""
    logger.info("=== [Level 2] Verifying Experiment & WorkUnits via ESS gRPC API ===")
    stub, channel = xmc_entities_validator.create_authenticated_stub("127.0.0.1:50051")
    try:
        experiment = xmc_entities_validator.validate_experiment(
            stub=stub,
            xid=xid,
            expected_title_pattern=EXPECTED_TITLE_PATTERN,
        )
        logger.info(
            "  [✓] Verified Experiment: %s (Title: '%s')", experiment.name, experiment.title
        )

        wu_results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid=xid,
            expected_count=EXPECTED_WORK_UNITS,
            expected_queue=EXPECTED_QUEUE,
            expected_worker_names=EXPECTED_WORKER_NAMES,
            timeout_sec=300,
        )
        for wu in wu_results:
            logger.info(
                "  [✓] Verified WorkUnit #%s: Worker='%s', Queue='%s', State='%s'",
                wu["id"],
                wu["worker_name"],
                wu["queue"],
                wu["state"],
            )
        return experiment, wu_results
    finally:
        if channel is not None and hasattr(channel, "close"):
            channel.close()


def _verify_demo_workload_ui(
    page: Page,
    xid: str,
    screenshots_dir: Path,
) -> None:
    """Verifies experiment navigation, workunit list, and status rendering in Web UI."""
    logger.info("=== [Level 3] Navigating to Experiment View in Web UI ===")
    page.goto(f"{BASE_URL}/experiments/{xid}", wait_until="domcontentloaded")
    page.get_by_text(EXPECTED_TITLE_PATTERN).first.wait_for(
        state="visible", timeout=DEFAULT_TIMEOUT_MS
    )
    save_screenshot(page, screenshots_dir, "01_demo_workload_experiment_view")

    logger.info("=== [Level 4] Verifying WorkUnits Rendering in Web UI ===")
    for worker_name in EXPECTED_WORKER_NAMES:
        worker_loc = page.get_by_text(worker_name).first
        expect(worker_loc).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
        logger.info("✓ Verified worker '%s' is visible in UI.", worker_name)

    save_screenshot(page, screenshots_dir, "02_demo_workload_workunits_view")
    logger.info("✓ Navigated to experiment view and verified UI rendering.")


def test_demo_workload_lifecycle(
    page: Page,
    screenshots_dir: Path,
    auth: DexAuthenticator,
) -> dict[str, Any]:
    """Tests executing, backend validation, and UI rendering of the Demo Workload.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.

    Returns:
        Dictionary with created experiment metadata and verification status.
    """
    launch_result = _launch_demo_workload(page, auth, screenshots_dir)

    experiment, wu_results = _verify_demo_workload_backend(launch_result.xid)

    _verify_demo_workload_ui(
        page=page,
        xid=launch_result.xid,
        screenshots_dir=screenshots_dir,
    )

    return {
        "xid": launch_result.xid,
        "experiment_name": experiment.name,
        "work_units_verified": len(wu_results),
    }


def main() -> None:
    """Executes the Demo Workload Tier 2 E2E test."""
    screenshots_dir = get_screenshots_dir()
    trace_handler = setup_logging(screenshots_dir)
    auth = DexAuthenticator(base_url=BASE_URL, dex_url=DEX_URL)

    logger.info("Starting Demo Workload Tier 2 E2E test...")
    with sync_playwright() as p, p.chromium.launch(headless=True) as browser:
        try:
            with browser.new_context(
                viewport={"width": 1920, "height": 1200},
                device_scale_factor=1,
            ) as context:
                page = context.new_page()
                test_demo_workload_lifecycle(page, screenshots_dir, auth)
            logger.info("==========================================================")
            logger.info("🎉 DEMO WORKLOAD TIER 2 E2E TEST PASSED SUCCESSFULLY! 🎉")
            logger.info("==========================================================")
        except Exception:
            logger.exception("Demo Workload Tier 2 E2E test failed!")
            sys.exit(1)
        finally:
            trace_handler.close()
            logging.getLogger().removeHandler(trace_handler)


if __name__ == "__main__":
    main()
