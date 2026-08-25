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

import dataclasses
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

UNAUTHORIZED_USER = "unauthorized@xmc.local"
UNAUTHORIZED_PASSWORD = "password123"

logger = logging.getLogger(__name__)


@dataclasses.dataclass(frozen=True)
class AdminResources:
    """Encapsulates admin-owned test resources for downstream boundary verification.

    Attributes:
        experiment_name: Canonical resource name of the admin experiment.
        artifact_name: Canonical resource name of the admin artifact.
    """

    experiment_name: str
    artifact_name: str


def _wait_for_dex_redirect(page: Page) -> None:
    """Waits for redirection from Dex IdP back to XMC dashboard."""
    dex_parsed = urllib.parse.urlparse(DEX_URL)
    current_parsed = urllib.parse.urlparse(page.url)
    if "dex" in page.url or current_parsed.netloc == dex_parsed.netloc:
        logger.info("Waiting for redirect from Dex back to XMC dashboard...")
        page.wait_for_url(
            lambda u: urllib.parse.urlparse(u).netloc != dex_parsed.netloc and "dex" not in u,
            timeout=DEFAULT_TIMEOUT_MS,
        )


def test_admin_flow_and_experiment_lifecycle(
    page: Page, screenshots_dir: Path, auth: DexAuthenticator
) -> AdminResources:
    """Tests admin authentication, safety banner, and experiment/work unit creation.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.

    Returns:
        AdminResources containing the created experiment and artifact names.

    Raises:
        RuntimeError: If experiment or work unit creation API fails.
    """
    logger.info("=== Testing Admin Authentication and Experiment Creation Lifecycle ===")
    auth.authenticate(page, ADMIN_USER, ADMIN_PASSWORD, screenshots_dir)
    _wait_for_dex_redirect(page)

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

    # 3. Create private experiment via API with admin-only ACLs
    experiment_title = f"E2E Automated Experiment {int(time.time())}"
    logger.info("Creating experiment via REST API: %s", experiment_title)

    payload: dict[str, Any] = {
        "title": experiment_title,
        "acls": {
            "owners": [{"user": ADMIN_USER}],
            "writers": [{"user": ADMIN_USER}],
            "readers": [{"user": ADMIN_USER}],
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

    # 4. Create an artifact under the admin experiment
    logger.info("Creating artifact under experiment %s...", experiment_name)
    art_response = page.request.post(
        f"{BASE_URL}/v1/{experiment_name}/artifacts",
        data={"url": {"url": "https://example.com/admin-artifact"}},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if art_response.status not in (200, 201):
        raise RuntimeError(
            f"Failed to create artifact via API: {art_response.status} {art_response.text()}"
        )
    art_json = art_response.json()
    admin_art_name = art_json.get("name", "")
    logger.info("✓ Created admin artifact: %s", admin_art_name)

    # 5. Assert experiment appears in the UI table
    logger.info("Reloading dashboard to verify experiment in table...")
    page.reload()

    exp_cell = page.get_by_text(experiment_title)
    expect(exp_cell).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Experiment '%s' row verified in DataTable.", experiment_title)
    save_screenshot(page, screenshots_dir, "02_experiment_in_datatable")

    return AdminResources(
        experiment_name=experiment_name,
        artifact_name=admin_art_name,
    )


def test_unauthorized_persona_boundary(
    page: Page, screenshots_dir: Path, auth: DexAuthenticator
) -> None:
    """Tests that unauthorized persona (unauthorized@xmc.local) is rejected with Access Denied.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.
    """
    logger.info("=== Testing Unauthorized Persona Boundary (unauthorized@xmc.local) ===")

    # Login as unauthorized persona (runs in isolated context)
    auth.authenticate(page, UNAUTHORIZED_USER, UNAUTHORIZED_PASSWORD, screenshots_dir)

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


def test_viewer_permission_boundaries_and_isolation(
    page: Page,
    screenshots_dir: Path,
    auth: DexAuthenticator,
    admin_resources: AdminResources,
) -> None:
    """Tests that a viewer persona is isolated from admin resources but can create own experiments.

    Args:
        page: Active Playwright Page instance.
        screenshots_dir: Destination directory for diagnostic screenshots.
        auth: DexAuthenticator instance for OIDC authentication.
        admin_resources: AdminResources holding the admin experiment and artifact names.

    Raises:
        RuntimeError: If negative boundary checks or experiment creation fail.
    """
    logger.info("=== Testing Viewer Permission Boundaries and Isolation ===")

    # 1. Authenticate viewer persona
    auth.authenticate(page, VIEWER_USER, VIEWER_PASSWORD, screenshots_dir)
    _wait_for_dex_redirect(page)

    # 2. Assert user reaches dashboard
    search_input = page.locator("input[data-testid='search-input-field']")
    expect(search_input).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info("✓ Viewer reached dashboard successfully.")
    save_screenshot(page, screenshots_dir, "04_viewer_dashboard_accessible")

    exp_path = admin_resources.experiment_name
    # In ESS, work unit mutations (:stop, :restart) authorize against the parent experiment (xid)
    # before attempting storage lookup. Thus, testing with a standard work unit path exercises
    # the negative authorization gate at the experiment boundary without needing full JobSet creation.
    wu_path = f"{exp_path}/workUnits/1"

    # 3. Assert GET admin experiment returns 403
    logger.info("Asserting negative permission: GET %s -> 403...", exp_path)
    get_res = page.request.get(f"{BASE_URL}/v1/{exp_path}")
    if get_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for GET {exp_path}, got {get_res.status}: {get_res.text()}"
        )
    logger.info("✓ GET %s returned 403 as expected.", exp_path)

    # 4. Assert POST work units under admin experiment returns 403
    logger.info("Asserting negative permission: POST %s/workUnits -> 403...", exp_path)
    post_wu_res = page.request.post(
        f"{BASE_URL}/v1/{exp_path}/workUnits",
        data={"title": "Viewer Unauthorized Work Unit"},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if post_wu_res.status in (200, 201):
        raise RuntimeError(
            "Security vulnerability: Viewer successfully created a work unit under admin experiment via workUnits endpoint!"
        )
    if post_wu_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for POST work units under {exp_path}, got {post_wu_res.status}: {post_wu_res.text()}"
        )
    logger.info("✓ POST work units under admin exp returned 403 as expected.")

    # 5. Assert PATCH admin experiment returns 403
    logger.info("Asserting negative permission: PATCH %s -> 403...", exp_path)
    patch_res = page.request.patch(
        f"{BASE_URL}/v1/{exp_path}",
        data={"title": "Tampered Title"},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if patch_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for PATCH {exp_path}, got {patch_res.status}: {patch_res.text()}"
        )
    logger.info("✓ PATCH %s returned 403 as expected.", exp_path)

    # 6. Assert POST work unit stop returns 403
    logger.info("Asserting negative permission: POST %s:stop -> 403...", wu_path)
    stop_res = page.request.post(
        f"{BASE_URL}/v1/{wu_path}:stop",
        data={},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if stop_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for POST {wu_path}:stop, got {stop_res.status}: {stop_res.text()}"
        )
    logger.info("✓ POST %s:stop returned 403 as expected.", wu_path)

    # 7. Assert POST work unit restart returns 403
    logger.info("Asserting negative permission: POST %s:restart -> 403...", wu_path)
    restart_res = page.request.post(
        f"{BASE_URL}/v1/{wu_path}:restart",
        data={},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if restart_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for POST {wu_path}:restart, got {restart_res.status}: {restart_res.text()}"
        )
    logger.info("✓ POST %s:restart returned 403 as expected.", wu_path)

    # 8. Assert POST artifacts under admin experiment returns 403
    logger.info("Asserting negative permission: POST %s/artifacts -> 403...", exp_path)
    art_res = page.request.post(
        f"{BASE_URL}/v1/{exp_path}/artifacts",
        data={"url": {"url": "https://example.com/unauthorized-artifact"}},
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if art_res.status in (200, 201):
        raise RuntimeError(
            "Security vulnerability: Viewer successfully created an artifact under admin experiment!"
        )
    if art_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for POST {exp_path}/artifacts, got {art_res.status}: {art_res.text()}"
        )
    logger.info("✓ POST %s/artifacts returned 403 as expected.", exp_path)

    # 9. Assert DELETE artifacts returns 403
    logger.info(
        "Asserting negative permission: DELETE %s -> 403...",
        admin_resources.artifact_name,
    )
    del_res = page.request.delete(
        f"{BASE_URL}/v1/{admin_resources.artifact_name}",
        headers={"Origin": BASE_URL},
    )
    if del_res.status in (200, 204):
        raise RuntimeError(
            f"Security vulnerability: Viewer successfully deleted admin artifact {admin_resources.artifact_name}!"
        )
    if del_res.status != 403:
        raise RuntimeError(
            f"Expected status 403 for DELETE artifact, got {del_res.status}: {del_res.text()}"
        )
    logger.info("✓ DELETE artifact returned 403 as expected.")

    # 10. Assert viewer can create their own experiment
    viewer_title = f"Viewer Private Experiment {int(time.time())}"
    logger.info("Creating viewer's own experiment: %s...", viewer_title)
    viewer_payload: dict[str, Any] = {
        "title": viewer_title,
        "acls": {
            "owners": [{"user": VIEWER_USER}],
            "writers": [{"user": VIEWER_USER}],
            "readers": [{"user": VIEWER_USER}],
        },
        "priority": "PRIORITY_MEDIUM",
    }
    viewer_create_res = page.request.post(
        f"{BASE_URL}/v1/experiments",
        data=viewer_payload,
        headers={"Content-Type": "application/json", "Origin": BASE_URL},
    )
    if viewer_create_res.status not in (200, 201):
        raise RuntimeError(
            f"Failed to create viewer experiment: {viewer_create_res.status} {viewer_create_res.text()}"
        )
    logger.info("✓ Viewer created own experiment successfully: %s", viewer_create_res.json())
    save_screenshot(page, screenshots_dir, "05_viewer_own_experiment_created")


def test_token_expiration_and_unauthenticated_boundary(page: Page, screenshots_dir: Path) -> None:
    """Tests that unauthenticated requests receive 401 and UI redirects to landing page.

    Args:
        page: Active Playwright Page instance (isolated clean context).
        screenshots_dir: Destination directory for diagnostic screenshots.

    Raises:
        RuntimeError: If unauthenticated API requests do not return 401.
    """
    logger.info("=== Testing Token Expiration and Unauthenticated Boundary ===")

    # 1. Unauthenticated /api/user check
    logger.info("Asserting unauthenticated GET /api/user -> 401...")
    user_res = page.request.get(f"{BASE_URL}/api/user")
    if user_res.status != 401:
        raise RuntimeError(
            f"Expected status 401 for unauthenticated GET /api/user, got {user_res.status}: {user_res.text()}"
        )
    logger.info("✓ Unauthenticated /api/user returned 401.")

    # 2. Unauthenticated /v1/experiments check
    logger.info("Asserting unauthenticated GET /v1/experiments -> 401...")
    exp_res = page.request.get(f"{BASE_URL}/v1/experiments")
    if exp_res.status != 401:
        raise RuntimeError(
            f"Expected status 401 for unauthenticated GET /v1/experiments, got {exp_res.status}: {exp_res.text()}"
        )
    logger.info("✓ Unauthenticated /v1/experiments returned 401.")

    # 3. Invalid / expired bearer token check
    logger.info("Asserting invalid bearer token GET /api/user -> 401...")
    invalid_token_res = page.request.get(
        f"{BASE_URL}/api/user",
        headers={"Authorization": "Bearer invalid_expired_jwt_token"},
    )
    if invalid_token_res.status != 401:
        raise RuntimeError(
            f"Expected status 401 for invalid bearer token, got {invalid_token_res.status}: {invalid_token_res.text()}"
        )
    logger.info("✓ Invalid bearer token returned 401.")

    # 4. Direct UI navigation to protected route (/experiments)
    logger.info("Navigating to protected route /experiments unauthenticated...")
    page.goto(f"{BASE_URL}/experiments")
    page.wait_for_url(
        lambda u: urllib.parse.urlparse(u).path in ("", "/"),
        timeout=DEFAULT_TIMEOUT_MS,
    )
    sign_in_btn = page.locator("button:has-text('Sign in')")
    expect(sign_in_btn).to_be_visible(timeout=DEFAULT_TIMEOUT_MS)
    logger.info(
        "✓ Unauthenticated UI navigation redirected to landing page with Sign in button visible."
    )
    save_screenshot(page, screenshots_dir, "06_unauthenticated_redirect_to_landing")


def _run_isolated_stage(browser: Any, stage_name: str, test_func: Any, *args: Any) -> Any:
    """Runs a test stage within a dedicated isolated browser context."""
    logger.info("--- %s ---", stage_name)
    with browser.new_context(viewport={"width": 1280, "height": 800}) as ctx:
        page = ctx.new_page()
        try:
            return test_func(page, *args)
        except Exception:
            try:
                url = page.url
                if args and isinstance(args[0], Path):
                    safe_stage_name = "".join(
                        c if c.isalnum() or c in "._-" else "_" for c in stage_name
                    )
                    save_screenshot(page, args[0], f"failure_{safe_stage_name}")
            except Exception as e:
                logger.warning("Failed to capture failure screenshot: %s", e)
                url = "<unknown>"
            logger.exception("%s failed at URL: %s", stage_name, url)
            raise


def main() -> None:
    """Executes the Playwright test suite against the local KinD cluster."""
    screenshots_dir = get_screenshots_dir()
    trace_handler = setup_logging(screenshots_dir)
    auth = DexAuthenticator(base_url=BASE_URL, dex_url=DEX_URL)

    logger.info("Starting headless Playwright E2E test execution...")
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)

        try:
            # 1. Admin Flow in an isolated context
            admin_resources: AdminResources = _run_isolated_stage(
                browser,
                "[1/4] Running Admin Flow",
                test_admin_flow_and_experiment_lifecycle,
                screenshots_dir,
                auth,
            )

            # 2. Viewer Flow in an isolated context
            _run_isolated_stage(
                browser,
                "[2/4] Running Viewer Flow",
                test_viewer_permission_boundaries_and_isolation,
                screenshots_dir,
                auth,
                admin_resources,
            )

            # 3. Token Expiration & Unauthenticated Boundary Flow in an isolated context
            _run_isolated_stage(
                browser,
                "[3/4] Running Token Expiration & Unauthenticated Flow",
                test_token_expiration_and_unauthenticated_boundary,
                screenshots_dir,
            )

            # 4. Unauthorized Persona Boundary Flow in an isolated context
            _run_isolated_stage(
                browser,
                "[4/4] Running Unauthorized Persona Boundary Flow",
                test_unauthorized_persona_boundary,
                screenshots_dir,
                auth,
            )

            logger.info("==================================================")
            logger.info("🎉 ALL E2E PLAYWRIGHT TESTS PASSED SUCCESSFULLY! 🎉")
            logger.info("==================================================")
        except Exception:
            sys.exit(1)
        finally:
            browser.close()
            trace_handler.close()
            logging.getLogger().removeHandler(trace_handler)


if __name__ == "__main__":
    main()
