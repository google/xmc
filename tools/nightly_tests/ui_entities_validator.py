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
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from playwright.sync_api import Page


class UIVerificationError(AssertionError):
    """Raised when an assertion against the Web UI DOM or state fails."""


DEFAULT_SCREENSHOTS_DIR: str = "/tmp/xmc_screenshots"

try:
    import playwright.sync_api as _playwright_sync_api

    sync_playwright: Any = _playwright_sync_api.sync_playwright
except ImportError:
    sync_playwright = None


class XMCUIBrowserSession:
    """Context manager managing an authenticated Playwright browser session for XMC."""

    def __init__(
        self,
        ui_base_url: str,
        auth_token: str | None = None,
        session_token: str | None = None,
        allow_insecure_tls: bool = False,
        headless: bool = True,
        screenshots_dir: str | None = None,
    ) -> None:
        """Initializes the browser session configuration.

        Args:
            ui_base_url: Base URL of the target XMC Web UI.
            auth_token: Optional Bearer or Cloud IAP OIDC token.
            session_token: Optional pre-authenticated session cookie value.
            allow_insecure_tls: Whether to disable TLS validation (default False).
            headless: Whether to launch Chromium in headless mode.
            screenshots_dir: Directory path where diagnostic screenshots are saved.
        """
        self.ui_base_url = ui_base_url.rstrip("/")
        self.auth_token = auth_token or os.environ.get("XMC_AUTH_TOKEN")
        self.session_token = session_token or os.environ.get("XMC_SESSION_TOKEN")
        self.allow_insecure_tls = allow_insecure_tls
        self.headless = headless
        self.screenshots_dir = screenshots_dir or os.environ.get(
            "XMC_SCREENSHOTS_DIR", DEFAULT_SCREENSHOTS_DIR
        )
        p = urllib.parse.urlparse(self.ui_base_url)
        self.domain_host = p.hostname or "localhost"
        self.scheme = p.scheme or "http"
        self.port = p.port
        self._playwright: Any = None
        self._browser: Any = None
        self._context: Any = None
        self._page: Any = None
        self.page_errors: list[str] = []
        self.http_errors: list[str] = []

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

    def _is_same_origin(self, url: str) -> bool:
        """Checks whether a request or response URL matches the configured UI origin."""
        p = urllib.parse.urlparse(url)
        b_port = self.port or (
            443 if self.scheme == "https" else (80 if self.scheme == "http" else None)
        )
        r_port = p.port or (443 if p.scheme == "https" else (80 if p.scheme == "http" else None))
        return p.hostname == self.domain_host and p.scheme == self.scheme and r_port == b_port

    def _configure_page_interception(self) -> None:
        """Configures domain-scoped Bearer header injection and diagnostic event listeners."""
        if self.auth_token:
            token = self.auth_token

            def _handle_route(route: Any) -> None:
                if self._is_same_origin(route.request.url):
                    route.continue_(
                        headers={**route.request.headers, "authorization": f"Bearer {token}"}
                    )
                    return
                route.continue_()

            self._page.route("**/*", _handle_route)

        self._page.on("console", lambda m: print(f"  [Browser Console] {m.type.upper()}: {m.text}"))
        self._page.on("pageerror", lambda err: self.page_errors.append(str(err)))

        def _log_response(response: Any) -> None:
            if response.status >= 400 or "/api/" in response.url:
                safe_url = (
                    urllib.parse.urlsplit(response.url)._replace(query="", fragment="").geturl()
                )
                print(f"  [Network Response] {response.status} {safe_url}")
                if response.status >= 400 and self._is_same_origin(response.url):
                    self.http_errors.append(f"{response.status} {safe_url}")

        self._page.on("response", _log_response)

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
                ignore_https_errors=self.allow_insecure_tls,
                viewport={"width": 1920, "height": 1080},
                device_scale_factor=2,
            )
            if cookies := self._build_auth_cookies():
                self._context.add_cookies(cookies)
            self._page = self._context.new_page()
            self._configure_page_interception()
        except Exception:
            self.close()
            raise

    def close(self) -> None:
        """Closes the browser page, context, browser instance, and Playwright driver safely."""
        self._page = None
        if self._context is not None:
            try:
                self._context.close()
            except Exception as e:
                print(f"  [Warning] Failed to close browser context: {e}")
            finally:
                self._context = None
        if self._browser is not None:
            try:
                self._browser.close()
            except Exception as e:
                print(f"  [Warning] Failed to close browser: {e}")
            finally:
                self._browser = None
        if self._playwright is not None:
            try:
                self._playwright.stop()
            except Exception as e:
                print(f"  [Warning] Failed to stop Playwright driver: {e}")
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

    def _assert_authenticated_landing(self, expected_path: str, error_tag: str) -> None:
        """Verifies the page did not land on an unauthorized view or unexpected redirect."""
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
        ):
            err_shot = self.take_screenshot(f"ui_AUTHENTICATION_REQUIRED_{error_tag}.png")
            raise UIVerificationError(
                f"❌ AUTHENTICATION REQUIRED: Landed on unauthorized or guest page "
                f"(url={self.page.url}). Screenshot -> {err_shot}"
            )
        if current_path != expected_path:
            err_shot = self.take_screenshot(f"ui_REDIRECT_{error_tag}.png")
            raise UIVerificationError(
                f"❌ UNEXPECTED REDIRECT: Expected path '{expected_path}' but landed on '{current_path}' "
                f"(url={self.page.url}). Screenshot -> {err_shot}"
            )

    def navigate_to_route(
        self,
        route_path: str,
        root_selector: str,
        screenshot_name: str,
        timeout_ms: int = 60000,
        error_tag: str | None = None,
    ) -> None:
        """Navigates to an arbitrary UI route and verifies authentication and landing path."""
        normalized_route = "/" + route_path.lstrip("/")
        target_url = f"{self.ui_base_url}{normalized_route}"
        expected_path = urllib.parse.urlparse(target_url).path.rstrip("/")
        print(f"\n[UI Action] Navigating to UI Route: {target_url}...")
        self.page.goto(target_url, wait_until="networkidle", timeout=timeout_ms)
        sel = f"{root_selector}, xmc-home-page, .guest-page-container"
        self.page.wait_for_selector(sel, timeout=timeout_ms)
        self.settle_and_wait_fonts()
        shot = self.take_screenshot(screenshot_name)
        print(f"  [✓] Route loaded. URL: {self.page.url} -> Screenshot: {shot}")
        tag = error_tag or normalized_route.strip("/").replace("/", "_") or "root"
        self._assert_authenticated_landing(expected_path, tag)

    def assert_no_runtime_errors(
        self, context_label: str, ignored_http_substrings: Sequence[str] = ()
    ) -> None:
        """Asserts that no uncaught page errors, same-origin HTTP >= 400 responses, or error banners exist."""
        banner_loc = self.page.locator("xmc-error-banner, .error-banner")
        banner_text = (
            (banner_loc.first.text_content() or "").strip() if banner_loc.count() > 0 else ""
        )
        unexpected_http = [
            e for e in self.http_errors if not any(sub in e for sub in ignored_http_substrings)
        ]
        if not self.page_errors and not unexpected_http and not banner_text:
            print(f"  [✓] Verified zero runtime/HTTP/banner errors ({context_label})")
            return
        err_shot = self.take_screenshot(f"ui_RUNTIME_ERROR_{context_label}.png")
        raise UIVerificationError(
            f"❌ RUNTIME ERROR ({context_label}): page_errors={self.page_errors}, "
            f"http_errors={unexpected_http}, error_banner='{banner_text}'. Screenshot -> {err_shot}"
        )

    def navigate_to_experiment(self, xid: str | int, timeout_ms: int = 60000) -> None:
        """Navigates to `/experiments/<xid>` and verifies that authentication succeeded."""
        self.navigate_to_route(
            f"/experiments/{xid}",
            "xmc-experiment-page",
            f"ui_step1_landing_xid_{xid}.png",
            timeout_ms=timeout_ms,
            error_tag=f"xid_{xid}",
        )

    def assert_experiment_header(
        self,
        expected_title_pattern: str | None = None,
        timeout_ms: int = 30000,
    ) -> str:
        """Asserts that the experiment title header is visible, non-empty, and matches expectations.

        Args:
            expected_title_pattern: Optional expected substring in the experiment header title.
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
        if expected_title_pattern and expected_title_pattern not in text:
            raise UIVerificationError(
                f"Expected header title pattern '{expected_title_pattern}' not found in DOM text: '{text}'"
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

        # Locate work unit rows
        rows_locator = self.page.locator(
            "xmc-work-units-table table tr.mat-mdc-row:not(.detail-row), xmc-work-units-table tr.mat-row:not(.detail-row)"
        )
        extracted_texts = [(text or "").strip() for text in rows_locator.all_inner_texts()]
        row_count = len(extracted_texts)

        if row_count < min_units:
            raise UIVerificationError(
                f"Expected at least {min_units} work unit row(s), but found {row_count} in the table."
            )

        for idx, text in enumerate(extracted_texts, 1):
            print(f"  [✓] Work Unit Row #{idx}: {text.replace(chr(10), ' ')}")

        if expected_names:
            print(
                f"\n[UI Assertion] Validating {len(expected_names)} expected work unit name pattern(s)..."
            )
            for pattern in expected_names:
                try:
                    regex = re.compile(pattern)
                except re.error:
                    regex = None
                matched = any(
                    pattern in row_text
                    or (regex is not None and regex.search(row_text) is not None)
                    for row_text in extracted_texts
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
