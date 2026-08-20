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

"""End-to-End Headless Playwright Test Suite for XMC.

Validates end-to-end user workflows against a local KinD cluster running XMC,
Dex OIDC authentication, Spanner repository storage, and the Angular UI.
"""

from __future__ import annotations

import logging
import os
import sys
import time
import urllib.parse
from pathlib import Path
from typing import Any

from playwright.sync_api import Page, expect, sync_playwright

from test.e2e.lib.dex_authenticator import DexAuthenticator
from test.e2e.lib.ui_diagnostics import (
    get_screenshots_dir,
    save_screenshot,
    setup_logging,
)

DEFAULT_TIMEOUT_MS = 15_000
NAVIGATION_TIMEOUT_MS = 10_000
UNAUTHORIZED_TIMEOUT_MS = 20_000

BASE_URL = os.getenv("XMC_BASE_URL", "http://localhost:8080")
DEX_URL = os.getenv("XMC_DEX_URL", "http://localhost:5556")

ADMIN_USER = "admin@xmc.local"
ADMIN_PASSWORD = "password123"

VIEWER_USER = "viewer@xmc.local"
VIEWER_PASSWORD = "password123"

logger = logging.getLogger(__name__)


def test_admin_flow_and_experiment_lifecycle(
    page: Page, screenshots_dir: Path, auth: DexAuthenticator
) -> None:
    """Tests admin authentication, safety banner, and experiment creation.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.

    Raises:
        RuntimeError: If experiment creation API fails.
    """
    logger.info("=== Testing Admin Authentication and Experiment Creation Lifecycle ===")
    auth.authenticate(page, ADMIN_USER, ADMIN_PASSWORD, screenshots_dir)

    dex_parsed = urllib.parse.urlparse(DEX_URL)
    current_parsed = urllib.parse.urlparse(page.url)
    if "dex" in page.url or current_parsed.netloc == dex_parsed.netloc:
        logger.info("Waiting for redirect from Dex back to XMC dashboard...")
        page.wait_for_url(
            lambda u: urllib.parse.urlparse(u).netloc != dex_parsed.netloc and "dex" not in u,
            timeout=DEFAULT_TIMEOUT_MS,
        )

    # 1. Assert Search Input field
    logger.info("Verifying authorization: search input field...")
    search_input = page.locator("input[data-testid='search-input-field']")
    expect(search_input).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Search input field is visible.")

    # 2. Assert Non-Production Safety Warning Banner
    logger.info("Verifying non-production safety warning banner...")
    banner = page.get_by_text("NON-PRODUCTION AUTHENTICATION ACTIVE")
    expect(banner).to_be_visible(timeout=NAVIGATION_TIMEOUT_MS)
    logger.info("✓ Non-production safety warning banner verified.")
    save_screenshot(page, screenshots_dir, "01_dashboard_with_warning_banner")

    # 3. Create experiment via API using authenticated context
    experiment_title = f"E2E Automated Experiment {int(time.time())}"
    logger.info("Creating experiment via REST API: %s", experiment_title)

    payload: dict[str, Any] = {
        "title": experiment_title,
        "acls": {
            "owners": [{"user": ADMIN_USER}],
            "writers": [{"user": ADMIN_USER}],
            "readers": [{"allAuthenticatedUsers": True}],
        },
        "priority": "PRIORITY_MEDIUM",
    }

    response = page.request.post(
        f"{BASE_URL}/v1/experiments",
        data=payload,
        headers={
            "Content-Type": "application/json",
            "Origin": BASE_URL,
        },
    )
    logger.info(
        "API creation response status: %d, body: %s",
        response.status,
        response.text(),
    )
    if response.status not in (200, 201):
        raise RuntimeError(
            f"Failed to create experiment via API: {response.status} {response.text()}"
        )

    res_json = response.json()
    logger.info("Created experiment response: %s", res_json)
    experiment_name = res_json.get("name", "")
    logger.info("✓ Created experiment successfully: %s", experiment_name)

    # 4. Assert experiment appears in the UI table
    logger.info("Reloading dashboard to verify experiment in table...")
    page.reload()

    exp_cell = page.get_by_text(experiment_title)
    expect(exp_cell).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Experiment '%s' row verified in DataTable.", experiment_title)
    save_screenshot(page, screenshots_dir, "02_experiment_in_datatable")


def test_viewer_unauthorized_boundary(
    page: Page, screenshots_dir: Path, auth: DexAuthenticator
) -> None:
    """Tests that unauthorized persona (viewer@xmc.local) is rejected with Access Denied.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.
    """
    logger.info("=== Testing Unauthorized Persona Boundary (viewer@xmc.local) ===")

    # Login as unauthorized persona (runs in isolated context)
    auth.authenticate(page, VIEWER_USER, VIEWER_PASSWORD, screenshots_dir)

    # Wait for redirection to landing page with error parameter
    logger.info(
        "Verifying access denial for unauthorized persona (current URL: %s)...",
        page.url,
    )
    page.wait_for_url(lambda u: "error=" in u, timeout=UNAUTHORIZED_TIMEOUT_MS)

    # Assert Access Denied banner is displayed
    access_denied_heading = page.locator(".error-banner h4, h4:has-text('Access Denied')").first
    expect(access_denied_heading).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ 'Access Denied' heading is visible.")

    # Assert error banner container is displayed with access rejection notice
    error_banner = page.locator(".error-banner").first
    expect(error_banner).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Error banner text confirmed: %s", error_banner.inner_text())
    save_screenshot(page, screenshots_dir, "03_access_denied_unauthorized_persona")


def main() -> None:
    """Executes the Playwright test suite against the local KinD cluster."""
    screenshots_dir = get_screenshots_dir()
    trace_handler = setup_logging(screenshots_dir)
    auth = DexAuthenticator(base_url=BASE_URL, dex_url=DEX_URL)

    logger.info("Starting headless Playwright E2E test execution...")
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)

        try:
            # Run Admin Flow in an isolated context
            with browser.new_context(viewport={"width": 1280, "height": 800}) as admin_ctx:
                admin_page = admin_ctx.new_page()
                try:
                    test_admin_flow_and_experiment_lifecycle(admin_page, screenshots_dir, auth)
                except Exception:
                    try:
                        url = admin_page.url
                    except Exception:
                        url = "<unknown>"
                    logger.exception("Admin flow failed at URL: %s", url)
                    raise

            # Run Viewer Flow in an isolated context
            with browser.new_context(viewport={"width": 1280, "height": 800}) as viewer_ctx:
                viewer_page = viewer_ctx.new_page()
                try:
                    test_viewer_unauthorized_boundary(viewer_page, screenshots_dir, auth)
                except Exception:
                    try:
                        url = viewer_page.url
                    except Exception:
                        url = "<unknown>"
                    logger.exception("Viewer flow failed at URL: %s", url)
                    raise

            logger.info("==================================================")
            logger.info("🎉 ALL E2E PLAYWRIGHT TESTS PASSED SUCCESSFULLY! 🎉")
            logger.info("==================================================")
        except Exception:
            sys.exit(1)
        finally:
            browser.close()
            trace_handler.close()
            logger.removeHandler(trace_handler)


if __name__ == "__main__":
    main()
