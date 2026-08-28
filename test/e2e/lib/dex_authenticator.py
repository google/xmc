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

"""Dex OpenID Connect IdP authentication automator for Playwright tests."""

from __future__ import annotations

import logging
import urllib.parse
from pathlib import Path
from typing import TYPE_CHECKING, Any

from test.e2e.lib.ui_diagnostics import save_screenshot

if TYPE_CHECKING:
    from playwright.sync_api import Page

logger = logging.getLogger(__name__)

DEFAULT_TIMEOUT_MS = 15_000
SHORT_TIMEOUT_MS = 3_000
NAVIGATION_TIMEOUT_MS = 10_000


class DexAuthenticator:
    """Automates user sign-in flows through Dex OpenID Connect IdP."""

    def __init__(
        self,
        base_url: str = "http://localhost:8080",
        dex_url: str = "http://localhost:5556",
    ) -> None:
        self.base_url = base_url
        self.dex_url = dex_url
        self.dex_parsed = urllib.parse.urlparse(dex_url)

    def _navigate_to_landing_and_sign_in(
        self, page: Page | Any, screenshots_dir: Path, email: str
    ) -> None:
        """Navigates to the XMC landing page and initiates OIDC sign-in."""
        logger.info("Navigating to landing page: %s", self.base_url)
        page.goto(self.base_url)
        user_prefix = email.split("@")[0]
        save_screenshot(page, screenshots_dir, f"landing_page_{user_prefix}")

        logger.info("Clicking 'Sign in' button...")
        sign_in_btn = page.locator("button:has-text('Sign in')")
        sign_in_btn.wait_for(state="visible", timeout=NAVIGATION_TIMEOUT_MS)
        sign_in_btn.click()

        logger.info("Waiting for Dex auth page (current URL: %s)...", page.url)
        try:
            page.wait_for_url(
                lambda u: urllib.parse.urlparse(u).netloc == self.dex_parsed.netloc or "dex" in u,
                timeout=NAVIGATION_TIMEOUT_MS,
            )
        except Exception as e:
            logger.debug("Optional wait for Dex URL passed or timed out: %s", e)
        logger.info("Current URL after login click: %s", page.url)

    def _select_connector_if_present(self, page: Page | Any) -> None:
        """Clicks Dex connector selection if a provider list is displayed."""
        local_login_btn = page.locator(
            "a:has-text('Log in with'), button:has-text('Log in with'), a[href*='local']"
        ).first
        try:
            local_login_btn.click(timeout=SHORT_TIMEOUT_MS)
            logger.info("Clicked Dex connector selection.")
        except Exception as e:
            logger.debug("No connector selection needed: %s", e)

    def _fill_and_submit_credentials(
        self, page: Page | Any, screenshots_dir: Path, email: str, password: str
    ) -> None:
        """Fills credentials into the Dex login form and submits."""
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
        except Exception as e:
            logger.debug("Redirect wait finished or timed out: %s", e)
        logger.info("Current URL after credentials submission: %s", page.url)

    def _handle_consent_if_present(
        self, page: Page | Any, screenshots_dir: Path, email: str
    ) -> None:
        """Approves Dex OAuth consent / grant access prompt if presented."""
        if "/approval" in page.url:
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
                    lambda u: (
                        urllib.parse.urlparse(u).netloc != self.dex_parsed.netloc and "dex" not in u
                    ),
                    timeout=DEFAULT_TIMEOUT_MS,
                )
            except Exception as e:
                logger.debug("No grant access button or prompt resolved: %s", e)

    def authenticate(
        self, page: Page | Any, email: str, password: str, screenshots_dir: Path
    ) -> None:
        """Performs the full OpenID Connect login flow through Dex IdP.

        Args:
            page: Active Playwright Page instance.
            email: User email address.
            password: User password.
            screenshots_dir: Destination directory for diagnostic screenshots.
        """
        self._navigate_to_landing_and_sign_in(page, screenshots_dir, email)
        self._select_connector_if_present(page)
        self._fill_and_submit_credentials(page, screenshots_dir, email, password)
        self._handle_consent_if_present(page, screenshots_dir, email)
