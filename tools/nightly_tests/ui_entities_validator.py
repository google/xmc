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

"""Generic validator library for XMC Web UI and Angular components via Playwright."""

from __future__ import annotations

import os
import re
import urllib.parse
from collections.abc import Sequence
from pathlib import Path
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from playwright.sync_api import Page

    DEFAULT_DEV_PASSWORD: str = "password123"
    DexAuthenticator: Any = None
else:
    from test.e2e.lib.config import DEFAULT_DEV_PASSWORD
    from test.e2e.lib.dex_authenticator import DexAuthenticator


class UIVerificationError(AssertionError):
    """Raised when an assertion against the Web UI DOM or state fails."""


DEFAULT_USER_EMAIL: str = os.environ.get("XMC_USER_EMAIL", "test@example.com")
DEFAULT_SCREENSHOTS_DIR: str = os.environ.get("XMC_SCREENSHOTS_DIR", "/tmp/xmc_screenshots")

sync_playwright: Any = None
try:
    from playwright.sync_api import sync_playwright  # type: ignore[no-redef]
except ImportError:
    pass


class XMCUIBrowserSession:
    """Context manager managing an authenticated Playwright browser session for XMC."""

    def __init__(
        self,
        ui_base_url: str,
        auth_token: str | None = None,
        session_token: str | None = None,
        user_email: str | None = None,
        user_password: str | None = None,
        dex_url: str | None = None,
        headless: bool = True,
        screenshots_dir: str | None = None,
    ) -> None:
        """Initializes the browser session configuration.

        Args:
            ui_base_url: Base URL of the target XMC Web UI.
            auth_token: Optional Bearer or Cloud IAP OIDC token.
            session_token: Optional pre-authenticated session cookie value.
            user_email: Optional user email identity for Dex authentication.
            user_password: Optional user password for Dex authentication.
            dex_url: Optional Dex OIDC identity provider URL.
            headless: Whether to launch Chromium in headless mode.
            screenshots_dir: Directory path where diagnostic screenshots are saved.
        """
        self.ui_base_url = ui_base_url.rstrip("/")
        self.auth_token = auth_token or os.environ.get("XMC_AUTH_TOKEN")
        self.session_token = session_token or os.environ.get("XMC_SESSION_TOKEN")
        self.user_email = user_email or os.environ.get("XMC_USER_EMAIL", DEFAULT_USER_EMAIL)
        self.user_password = user_password or os.environ.get(
            "XMC_USER_PASSWORD", DEFAULT_DEV_PASSWORD
        )
        self.dex_url = dex_url or os.environ.get("XMC_DEX_URL")
        self.headless = headless
        self.screenshots_dir = screenshots_dir or os.environ.get(
            "XMC_SCREENSHOTS_DIR", DEFAULT_SCREENSHOTS_DIR
        )
        p = urllib.parse.urlparse(self.ui_base_url)
        self.domain_host, self.scheme = p.hostname or "localhost", p.scheme or "http"
        self._playwright: Any = None
        self._browser: Any = None
        self._context: Any = None
        self._page: Any = None

    @property
    def _playwright_mgr(self) -> Any:
        """Backward-compatible alias for the active Playwright instance."""
        return self._playwright

    @_playwright_mgr.setter
    def _playwright_mgr(self, value: Any) -> None:
        self._playwright = value

    def __enter__(self) -> XMCUIBrowserSession:
        """Starts the Playwright session upon entering the context manager."""
        self.start()
        return self

    def __exit__(self, *args: Any) -> None:
        """Closes the Playwright session upon exiting the context manager."""
        self.close()

    def _build_auth_cookies(self) -> list[dict[str, Any]]:
        """Builds domain-scoped authentication cookies for the browser context.

        Returns:
            A list of Playwright cookie dictionaries for non-empty tokens.
        """
        sec = self.scheme == "https"
        return [
            {
                "name": k,
                "value": v,
                "domain": self.domain_host,
                "path": "/",
                "httpOnly": True,
                "secure": sec,
            }
            for k, v in (
                ("GCP_IAAP_AUTH_TOKEN", self.auth_token),
                ("session_token", self.session_token),
            )
            if v
        ]

    def _configure_page_interception(self) -> None:
        """Configures domain-scoped Bearer header injection and diagnostic event listeners."""
        if self.auth_token:
            token, host = self.auth_token, self.domain_host
            self._page.route(
                "**/*",
                lambda r: (
                    r.continue_(headers={**r.request.headers, "authorization": f"Bearer {token}"})
                    if urllib.parse.urlparse(r.request.url).hostname == host
                    else r.continue_()
                ),
            )
        self._page.on("console", lambda m: print(f"  [Browser Console] {m.type.upper()}: {m.text}"))
        self._page.on(
            "response",
            lambda r: (
                print(f"  [Network Response] {r.status} {r.url}")
                if r.status >= 400 or "/api/" in r.url
                else None
            ),
        )

    def _authenticate_via_dex(self) -> None:
        """Executes OIDC login via DexAuthenticator when dex_url is provided without token auth."""
        if self.dex_url and not self.auth_token and not self.session_token:
            authenticator = DexAuthenticator(base_url=self.ui_base_url, dex_url=self.dex_url)
            authenticator.authenticate(
                self._page,
                self.user_email,
                self.user_password,
                Path(self.screenshots_dir),
            )

    def start(self) -> None:
        """Launches Chromium and configures the authenticated browser context.

        Raises:
            RuntimeError: If Playwright is not installed.
        """
        if sync_playwright is None:
            raise RuntimeError("Playwright is required. Install with `pip install playwright`.")
        os.makedirs(self.screenshots_dir, exist_ok=True)
        print(
            f"\n[UI Session] Playwright Chromium (headless={self.headless}) -> {self.ui_base_url}"
        )
        try:
            self._playwright = sync_playwright().start()
            self._browser = self._playwright.chromium.launch(
                headless=self.headless,
                args=["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
            )
            self._context = self._browser.new_context(
                ignore_https_errors=True,
                viewport={"width": 1920, "height": 1080},
                device_scale_factor=2,
            )
            if cookies := self._build_auth_cookies():
                self._context.add_cookies(cookies)
            self._page = self._context.new_page()
            self._configure_page_interception()
            self._authenticate_via_dex()
        except Exception:
            self.close()
            raise

    def close(self) -> None:
        """Closes the browser context, browser instance, and Playwright driver safely."""
        try:
            if self._context is not None:
                self._context.close()
        finally:
            self._context = None
            try:
                if self._browser is not None:
                    self._browser.close()
            finally:
                self._browser = None
                try:
                    if self._playwright is not None:
                        self._playwright.stop()
                finally:
                    self._playwright = None

    @property
    def page(self) -> Page:
        """Returns the active Playwright Page instance.

        Returns:
            The initialized Playwright Page.

        Raises:
            RuntimeError: If the browser session has not been started.
        """
        if self._page is None:
            raise RuntimeError("Browser session not started. Call start() or use context manager.")
        return self._page

    def settle_and_wait_fonts(self, timeout_ms: int = 500) -> None:
        """Waits for document fonts to finish loading and settles the UI for a brief duration.

        Args:
            timeout_ms: Additional settling wait time in milliseconds.
        """
        try:
            self.page.evaluate("() => document.fonts.ready")
        except Exception:
            pass
        self.page.wait_for_timeout(timeout_ms)

    def take_screenshot(self, filename: str, full_page: bool = True) -> str:
        """Captures a diagnostic screenshot of the current page state.

        Args:
            filename: Basename of the screenshot file to write inside `screenshots_dir`.
            full_page: Whether to capture the full scrollable page.

        Returns:
            The absolute or relative filesystem path to the saved screenshot.
        """
        self.settle_and_wait_fonts()
        path = os.path.join(self.screenshots_dir, filename)
        self.page.screenshot(path=path, full_page=full_page)
        return path

    def navigate_to_experiment(self, xid: str | int, timeout_ms: int = 60000) -> None:
        """Navigates to `/experiments/<xid>` and verifies that authentication succeeded.

        Args:
            xid: Experiment XID to navigate to.
            timeout_ms: Navigation and selector wait timeout in milliseconds.

        Raises:
            UIVerificationError: If redirected to guest login, home page, or an HTTP 401/403 page.
        """
        expected_path = f"/experiments/{xid}"
        target_url = f"{self.ui_base_url}{expected_path}"
        print(f"\n[UI Action] Navigating to Experiment Dashboard: {target_url}...")
        self.page.goto(target_url, wait_until="networkidle", timeout=timeout_ms)
        self.page.wait_for_selector(
            "xmc-experiment-page, xmc-home-page, .guest-page-container",
            timeout=timeout_ms,
        )
        self.settle_and_wait_fonts()
        shot = self.take_screenshot(f"ui_step1_landing_xid_{xid}.png")
        print(f"  [✓] Page loaded. URL: {self.page.url} -> Screenshot: {shot}")

        body = self.page.locator("body").inner_text() or ""
        current_path = urllib.parse.urlparse(self.page.url).path.rstrip("/")
        auth_error_markers = (
            "Sign in with Google",
            "Secure ML Experiment Management",
            "HTTP ERROR 401",
            "HTTP ERROR 403",
            "Access Denied",
        )
        if (
            any(marker in body for marker in auth_error_markers)
            or self.page.locator("xmc-home-page, .guest-page-container").count() != 0
            or current_path != expected_path
        ):
            err_shot = self.take_screenshot(f"ui_AUTHENTICATION_REQUIRED_xid_{xid}.png")
            raise UIVerificationError(
                f"❌ AUTHENTICATION REQUIRED: Landed on unauthorized or guest page "
                f"(url={self.page.url}). Screenshot -> {err_shot}"
            )

    def assert_experiment_header(
        self,
        expected_title_pattern: str | None = None,
        expected_name: str | None = None,
        xid: str | int | None = None,
        timeout_ms: int = 30000,
    ) -> str:
        """Asserts that the experiment title header is visible, non-empty, and matches expectations.

        Args:
            expected_title_pattern: Optional expected substring in the experiment header title.
            expected_name: Optional alias for expected experiment name substring.
            xid: Optional experiment XID expected to appear in the header text.
            timeout_ms: Visibility wait timeout in milliseconds.

        Returns:
            The stripped text content of the experiment header element.

        Raises:
            UIVerificationError: If the header text is empty or missing expected substrings.
        """
        print("\n[UI Assertion] Asserting Experiment Header rendering...")
        title_locator = self.page.locator("h1.experiment-title, .experiment-title").first
        title_locator.wait_for(state="visible", timeout=timeout_ms)
        self.settle_and_wait_fonts()
        text = (title_locator.text_content() or "").strip()
        if not text:
            raise UIVerificationError("Experiment header title element rendered with empty text.")
        target_pattern = expected_name if expected_name is not None else expected_title_pattern
        if target_pattern and target_pattern not in text:
            raise UIVerificationError(
                f"Expected header title pattern '{target_pattern}' not found in DOM text: '{text}'"
            )
        if xid is not None and str(xid) not in text:
            raise UIVerificationError(
                f"Expected experiment XID '{xid}' not found in DOM text: '{text}'"
            )
        print(f"  [✓] Verified Header Title DOM text: '{text}'")
        return text

    def switch_tab(self, tab_name: str, timeout_ms: int = 15000) -> None:
        """Clicks a navigation tab (e.g., 'Work Units', 'Status History').

        Args:
            tab_name: Visible label text of the Material tab to activate.
            timeout_ms: Visibility wait timeout in milliseconds.
        """
        print(f"\n[UI Action] Traversing to tab: '{tab_name}'...")
        tab_locator = self.page.locator(
            f'mat-tab-header .mdc-tab:has-text("{tab_name}"), .mat-tab-label:has-text("{tab_name}")'
        ).first
        tab_locator.wait_for(state="visible", timeout=timeout_ms)
        tab_locator.click()
        self.settle_and_wait_fonts()
        print(f"  [✓] Successfully activated tab: '{tab_name}'")

    def assert_work_units_tab(
        self,
        min_units: int = 1,
        expected_names: Sequence[str] | None = None,
        timeout_ms: int = 15000,
    ) -> list[str]:
        """Asserts that the Work Units tab table is visible with active unit rows.

        Args:
            min_units: Minimum number of work unit table rows expected.
            expected_names: Optional sequence of substrings or regex patterns to match against rows.
            timeout_ms: Table selector wait timeout in milliseconds.

        Returns:
            List of stripped row text strings from the Work Units table.

        Raises:
            UIVerificationError: If fewer than `min_units` rows exist or a pattern fails to match.
        """
        self.switch_tab("Work Units", timeout_ms=timeout_ms)
        self.page.wait_for_selector(
            "xmc-work-units-table, table.mat-mdc-table, mat-table",
            timeout=timeout_ms,
        )
        self.settle_and_wait_fonts()

        row_locators = self.page.locator(
            "xmc-work-units-table table tr.mat-mdc-row:not(.detail-row), xmc-work-units-table tr.mat-row:not(.detail-row)"
        ).all()
        row_count = len(row_locators)

        if row_count < min_units:
            raise UIVerificationError(
                f"Expected at least {min_units} work unit row(s), but found {row_count} in the table."
            )

        extracted_texts: list[str] = []
        for idx, row in enumerate(row_locators, 1):
            text = (row.inner_text() or "").strip()
            extracted_texts.append(text)
            print(f"  [✓] Work Unit Row #{idx}: {text.replace(chr(10), ' ')}")

        if expected_names:
            print(
                f"\n[UI Assertion] Validating {len(expected_names)} expected work unit name pattern(s)..."
            )
            for pattern in expected_names:
                regex = re.compile(pattern)
                matched = any(
                    regex.search(row_text) or pattern in row_text for row_text in extracted_texts
                )
                if not matched:
                    raise UIVerificationError(
                        f"Expected work unit name/pattern '{pattern}' did not match any of the {row_count} rendered work units: {extracted_texts}"
                    )
                print(f"  [✓] Verified expected work unit name pattern match: '{pattern}'")

        print(
            f"  [✓] Verified Work Units table rendering ({row_count} units found, min required: {min_units})"
        )
        return extracted_texts
