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
"""End-to-End Headless Playwright Test Suite for XMC."""

import logging
import os
import sys
import time
import traceback
from typing import Any

from playwright.sync_api import BrowserContext, Page, expect, sync_playwright

SCREENSHOTS_DIR = os.getenv(
    "XMC_SCREENSHOTS_DIR",
    "/usr/local/google/home/vluk/.gemini/jetski/brain/4eaf6530-cdad-4e4d-b9b2-c998778f24c5",
)
os.makedirs(SCREENSHOTS_DIR, exist_ok=True)

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
logger = logging.getLogger(__name__)

trace_handler = logging.FileHandler(
    os.path.join(SCREENSHOTS_DIR, "playwright_e2e_trace.txt"), mode="w"
)
trace_handler.setFormatter(logging.Formatter("%(asctime)s [%(levelname)s] %(message)s"))
logger.addHandler(trace_handler)

BASE_URL = "http://localhost:8080"
DEX_URL = "http://localhost:5556"

ADMIN_USER = "admin@xmc.local"
ADMIN_PASSWORD = "password123"

VIEWER_USER = "viewer@xmc.local"
VIEWER_PASSWORD = "password123"


def save_screenshot(page: Page, name: str) -> str:
    try:
        os.makedirs(SCREENSHOTS_DIR, exist_ok=True)
        path = os.path.join(SCREENSHOTS_DIR, f"{name}.png")
        page.screenshot(path=path, full_page=True)
        logger.info("📸 Saved screenshot: %s", path)
        return path
    except Exception as e:
        logger.warning("Failed to save screenshot %s: %s", name, e)
        return ""


def login_via_dex(page: Page, email: str, password: str) -> None:
    """Performs the full OpenID Connect login flow through Dex IdP."""
    logger.info("Navigating to landing page: %s", BASE_URL)
    page.goto(BASE_URL, wait_until="networkidle")
    save_screenshot(page, f"landing_page_{email.split('@')[0]}")

    # Click Sign In on landing page
    logger.info("Clicking 'Sign in' button...")
    sign_in_btn = page.locator("button:has-text('Sign in')")
    expect(sign_in_btn).to_be_visible(timeout=10000)
    sign_in_btn.click()

    logger.info("Waiting for Dex auth page (current URL: %s)...", page.url)
    try:
        page.wait_for_url(lambda u: "5556" in u or "dex" in u, timeout=10000)
    except Exception:
        pass
    logger.info("Current URL after login click: %s", page.url)

    # If Dex presents a connector choice (e.g. 'Log in with Email' or 'local')
    local_login_btn = page.locator(
        "a:has-text('Log in with'), button:has-text('Log in with'), a[href*='local']"
    ).first
    try:
        local_login_btn.click(timeout=3000)
        logger.info("Clicked Dex connector selection.")
    except Exception:
        pass

    # Wait for Dex login form
    logger.info("Waiting for Dex login form (current URL: %s)...", page.url)
    login_input = page.locator(
        "input#login, input[name='login'], input[type='email'], input[type='text']"
    )
    login_input.first.wait_for(state="visible", timeout=15000)
    logger.info("Dex login form visible at URL: %s", page.url)
    save_screenshot(page, f"dex_login_form_{email.split('@')[0]}")

    # Fill credentials
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
            lambda u: "/approval" in u or "8080" in u or "callback" in u or "experiments" in u,
            timeout=10000,
        )
    except Exception:
        pass
    logger.info("Current URL after credentials submission: %s", page.url)

    # If Dex presents an OAuth consent/approval screen (e.g. 'Grant Access')
    if "dex" in page.url or "5556" in page.url:
        save_screenshot(page, f"dex_approval_prompt_{email.split('@')[0]}")
        grant_btn = page.locator(
            "button:has-text('Grant Access'), button:has-text('Grant Approval'), button:has-text('Approval'), button:has-text('Allow'), button[type='submit']"
        ).first
        try:
            grant_btn.click(timeout=3000)
            logger.info("Clicked Dex grant access button.")
            page.wait_for_url(lambda u: "5556" not in u, timeout=15000)
        except Exception:
            pass


def test_admin_flow_and_experiment_lifecycle(context: BrowserContext, page: Page) -> None:
    """Tests admin authentication, safety banner, and experiment creation."""
    logger.info("=== Testing Admin Authentication and Experiment Creation Lifecycle ===")
    login_via_dex(page, ADMIN_USER, ADMIN_PASSWORD)

    # Wait for navigation back from Dex to XMC
    logger.info("Current URL after Dex auth: %s", page.url)
    if "5556" in page.url:
        logger.info("Waiting for redirect from Dex back to XMC dashboard...")
        page.wait_for_url(lambda u: "8080" in u, timeout=15000)

    # 1. Assert Search Input field
    logger.info("Verifying authorization: search input field...")
    search_input = page.locator("input[data-testid='search-input-field']")
    expect(search_input).to_be_visible(timeout=15000)
    logger.info("✓ Search input field is visible.")

    # 2. Assert Non-Production Safety Warning Banner
    logger.info("Verifying non-production safety warning banner...")
    banner = page.get_by_text("NON-PRODUCTION AUTHENTICATION ACTIVE")
    expect(banner).to_be_visible(timeout=10000)
    logger.info("✓ Non-production safety warning banner verified.")
    save_screenshot(page, "01_dashboard_with_warning_banner")

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
    logger.info("API creation response status: %d, body: %s", response.status, response.text())
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
    expect(exp_cell).to_be_visible(timeout=15000)
    logger.info("✓ Experiment '%s' row verified in DataTable.", experiment_title)
    save_screenshot(page, "02_experiment_in_datatable")


def test_viewer_unauthorized_boundary(page: Page) -> None:
    """Tests that unauthorized persona (viewer@xmc.local) is rejected with Access Denied."""
    logger.info("=== Testing Unauthorized Persona Boundary (viewer@xmc.local) ===")

    # Clear cookies/logout
    page.goto(f"{BASE_URL}/api/logout", wait_until="networkidle")
    page.context.clear_cookies()

    # Login as unauthorized persona
    login_via_dex(page, VIEWER_USER, VIEWER_PASSWORD)

    # Wait for redirection to landing page with error parameter
    logger.info("Verifying access denial for unauthorized persona (current URL: %s)...", page.url)
    page.wait_for_url(lambda u: "error=" in u, timeout=20000)

    # Assert Access Denied banner is displayed
    access_denied_heading = page.locator(".error-banner h4, h4:has-text('Access Denied')").first
    expect(access_denied_heading).to_be_visible(timeout=15000)
    logger.info("✓ 'Access Denied' heading is visible.")

    # Assert error banner container is displayed with access rejection notice
    error_banner = page.locator(".error-banner").first
    expect(error_banner).to_be_visible(timeout=15000)
    logger.info("✓ Error banner text confirmed: %s", error_banner.inner_text())
    save_screenshot(page, "03_access_denied_unauthorized_persona")


def main() -> None:
    logger.info("Starting headless Playwright E2E test execution...")
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(viewport={"width": 1280, "height": 800})
        page = context.new_page()

        try:
            test_admin_flow_and_experiment_lifecycle(context, page)
            test_viewer_unauthorized_boundary(page)
            logger.info("==================================================")
            logger.info("🎉 ALL E2E PLAYWRIGHT TESTS PASSED SUCCESSFULLY! 🎉")
            logger.info("==================================================")
        except Exception:
            logger.error("Test execution failed at URL: %s", page.url)
            try:
                error_log_path = os.path.join(SCREENSHOTS_DIR, "playwright_e2e_error.txt")
                with open(error_log_path, "w", encoding="utf-8") as ef:
                    ef.write(f"Failed at URL: {page.url}\n")
                    ef.write(traceback.format_exc())
            except Exception:
                pass
            traceback.print_exc()
            sys.exit(1)
        finally:
            context.close()
            browser.close()


if __name__ == "__main__":
    main()
