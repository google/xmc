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
import tempfile
import time
import urllib.parse
from pathlib import Path
from typing import Any

from playwright.sync_api import (
    Error as PlaywrightError,
    Page,
    TimeoutError as PlaywrightTimeoutError,
    expect,
    sync_playwright,
)

DEFAULT_TIMEOUT_MS = 15_000
SHORT_TIMEOUT_MS = 3_000
NAVIGATION_TIMEOUT_MS = 10_000
UNAUTHORIZED_TIMEOUT_MS = 20_000

BASE_URL = os.getenv("XMC_BASE_URL", "http://localhost:8080")
DEX_URL = os.getenv("XMC_DEX_URL", "http://localhost:5556")

ADMIN_USER = "admin@xmc.local"
ADMIN_PASSWORD = "password123"

VIEWER_USER = "viewer@xmc.local"
VIEWER_PASSWORD = "password123"

logger = logging.getLogger(__name__)


def get_screenshots_dir() -> Path:
    """Returns the resolved screenshots and diagnostic logs output directory.

    Returns:
        Path pointing to the output directory.
    """
    env_dir = os.getenv("XMC_SCREENSHOTS_DIR")
    if env_dir:
        screenshots_path = Path(env_dir)
    else:
        screenshots_path = Path(tempfile.gettempdir()) / "xmc_e2e_screenshots"
    screenshots_path.mkdir(parents=True, exist_ok=True)
    return screenshots_path


def setup_logging(screenshots_dir: Path) -> logging.Handler:
    """Configures console and file logging handlers for test execution.

    Args:
        screenshots_dir: Directory where diagnostic trace files will be saved.

    Returns:
        The configured file handler instance.
    """
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(message)s",
    )
    trace_path = screenshots_dir / "playwright_e2e_trace.txt"
    trace_handler = logging.FileHandler(trace_path, mode="w", encoding="utf-8")
    trace_handler.setFormatter(logging.Formatter("%(asctime)s [%(levelname)s] %(message)s"))
    logger.addHandler(trace_handler)
    return trace_handler


def save_screenshot(page: Page, screenshots_dir: Path, name: str) -> str:
    """Captures and saves a full-page screenshot for diagnostic logs.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory.
        name: Base filename prefix without extension.

    Returns:
        String path to the saved screenshot, or empty string on failure.
    """
    try:
        screenshots_dir.mkdir(parents=True, exist_ok=True)
        img_path = screenshots_dir / f"{name}.png"
        page.screenshot(path=str(img_path), full_page=True)
        logger.info("📸 Saved screenshot: %s", img_path)
        return str(img_path)
    except (PlaywrightError, OSError) as e:
        logger.warning("Failed to save screenshot %s: %s", name, e)
        return ""


def _navigate_to_landing_and_sign_in(page: Page, screenshots_dir: Path, email: str) -> None:
    """Navigates to the XMC landing page and initiates OIDC sign-in.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        email: User email identifier for diagnostic screenshot labelling.
    """
    logger.info("Navigating to landing page: %s", BASE_URL)
    page.goto(BASE_URL, wait_until="networkidle")
    user_prefix = email.split("@")[0]
    save_screenshot(page, screenshots_dir, f"landing_page_{user_prefix}")

    logger.info("Clicking 'Sign in' button...")
    sign_in_btn = page.locator("button:has-text('Sign in')")
    expect(sign_in_btn).to_be_visible(timeout=NAVIGATION_TIMEOUT_MS)
    sign_in_btn.click()

    logger.info("Waiting for Dex auth page (current URL: %s)...", page.url)
    dex_netloc = urllib.parse.urlparse(DEX_URL).netloc
    try:
        page.wait_for_url(
            lambda u: dex_netloc in u or "dex" in u,
            timeout=NAVIGATION_TIMEOUT_MS,
        )
    except (PlaywrightTimeoutError, PlaywrightError) as e:
        logger.debug("Optional wait for Dex URL passed or timed out: %s", e)
    logger.info("Current URL after login click: %s", page.url)


def _select_connector_if_present(page: Page) -> None:
    """Clicks Dex connector selection if a provider list is displayed.

    Args:
        page: Active Playwright Page instance.
    """
    local_login_btn = page.locator(
        "a:has-text('Log in with'), button:has-text('Log in with'), a[href*='local']"
    ).first
    try:
        local_login_btn.click(timeout=SHORT_TIMEOUT_MS)
        logger.info("Clicked Dex connector selection.")
    except (PlaywrightTimeoutError, PlaywrightError) as e:
        logger.debug("No connector selection needed: %s", e)


def _fill_and_submit_credentials(
    page: Page, screenshots_dir: Path, email: str, password: str
) -> None:
    """Fills credentials into the Dex login form and submits.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        email: User email address.
        password: User password.
    """
    logger.info("Waiting for Dex login form (current URL: %s)...", page.url)
    login_input = page.locator(
        "input#login, input[name='login'], input[type='email'], input[type='text']"
    )
    login_input.first.wait_for(state="visible", timeout=DEFAULT_TIMEOUT_MS)
    logger.info("Dex login form visible at URL: %s", page.url)
    user_prefix = email.split("@")[0]
    save_screenshot(page, screenshots_dir, f"dex_login_form_{user_prefix}")

    logger.info("Submitting credentials for user: %s", email)
    login_input.first.fill(email)
    page.locator("input#password, input[name='password'], input[type='password']").first.fill(
        password
    )
    logger.info("Submitting login form...")
    page.locator(
        "button:has-text('Log in'), button:has-text('Login'), button[type='submit']"
    ).first.click()

    try:
        page.wait_for_url(
            lambda u: "/approval" in u or "callback" in u or "experiments" in u,
            timeout=NAVIGATION_TIMEOUT_MS,
        )
    except (PlaywrightTimeoutError, PlaywrightError) as e:
        logger.debug("Redirect wait finished or timed out: %s", e)
    logger.info("Current URL after credentials submission: %s", page.url)


def _handle_consent_if_present(page: Page, screenshots_dir: Path, email: str) -> None:
    """Approves Dex OAuth consent / grant access prompt if presented.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        email: User email identifier for diagnostic screenshot labelling.
    """
    dex_netloc = urllib.parse.urlparse(DEX_URL).netloc
    if "dex" in page.url or dex_netloc in page.url:
        user_prefix = email.split("@")[0]
        save_screenshot(page, screenshots_dir, f"dex_approval_prompt_{user_prefix}")
        grant_btn = page.locator(
            "button:has-text('Grant Access'), button:has-text('Grant Approval'), "
            "button:has-text('Approval'), button:has-text('Allow'), button[type='submit']"
        ).first
        try:
            grant_btn.click(timeout=SHORT_TIMEOUT_MS)
            logger.info("Clicked Dex grant access button.")
            page.wait_for_url(
                lambda u: dex_netloc not in u and "dex" not in u,
                timeout=DEFAULT_TIMEOUT_MS,
            )
        except (PlaywrightTimeoutError, PlaywrightError) as e:
            logger.debug("No grant access button or prompt resolved: %s", e)


def login_via_dex(page: Page, screenshots_dir: Path, email: str, password: str) -> None:
    """Performs the full OpenID Connect login flow through Dex IdP.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        email: User email address.
        password: User password.
    """
    _navigate_to_landing_and_sign_in(page, screenshots_dir, email)
    _select_connector_if_present(page)
    _fill_and_submit_credentials(page, screenshots_dir, email, password)
    _handle_consent_if_present(page, screenshots_dir, email)


def test_admin_flow_and_experiment_lifecycle(page: Page, screenshots_dir: Path) -> None:
    """Tests admin authentication, safety banner, and experiment creation.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.

    Raises:
        RuntimeError: If experiment creation API fails.
    """
    logger.info("=== Testing Admin Authentication and Experiment Creation Lifecycle ===")
    login_via_dex(page, screenshots_dir, ADMIN_USER, ADMIN_PASSWORD)

    dex_netloc = urllib.parse.urlparse(DEX_URL).netloc
    if dex_netloc in page.url or "dex" in page.url:
        logger.info("Waiting for redirect from Dex back to XMC dashboard...")
        page.wait_for_url(
            lambda u: dex_netloc not in u and "dex" not in u,
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
    page.reload(wait_until="networkidle")

    exp_cell = page.get_by_text(experiment_title)
    expect(exp_cell).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Experiment '%s' row verified in DataTable.", experiment_title)
    save_screenshot(page, screenshots_dir, "02_experiment_in_datatable")


def test_viewer_unauthorized_boundary(page: Page, screenshots_dir: Path) -> None:
    """Tests that unauthorized persona (viewer@xmc.local) is rejected with Access Denied.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
    """
    logger.info("=== Testing Unauthorized Persona Boundary (viewer@xmc.local) ===")

    # Clear cookies/logout
    page.goto(f"{BASE_URL}/api/logout", wait_until="networkidle")
    page.context.clear_cookies()

    # Login as unauthorized persona
    login_via_dex(page, screenshots_dir, VIEWER_USER, VIEWER_PASSWORD)

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

    logger.info("Starting headless Playwright E2E test execution...")
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 1280, "height": 800})
        page = context.new_page()

        try:
            test_admin_flow_and_experiment_lifecycle(page, screenshots_dir)
            test_viewer_unauthorized_boundary(page, screenshots_dir)
            logger.info("==================================================")
            logger.info("🎉 ALL E2E PLAYWRIGHT TESTS PASSED SUCCESSFULLY! 🎉")
            logger.info("==================================================")
        except Exception:
            logger.exception("Test execution failed at URL: %s", page.url)
            sys.exit(1)
        finally:
            context.close()
            browser.close()
            trace_handler.close()
            logger.removeHandler(trace_handler)


if __name__ == "__main__":
    main()
