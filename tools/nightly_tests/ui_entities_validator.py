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

import base64
import datetime
import hashlib
import hmac
import json
import os
import urllib.parse
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from playwright.sync_api import Page


def _base64url_encode(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode("utf-8").rstrip("=")


def _base64url_decode(data: str) -> bytes:
    return base64.urlsafe_b64decode(data + "=" * (-len(data) % 4))


class UIVerificationError(AssertionError):
    """Raised when an assertion against the Web UI DOM or state fails."""


def generate_session_cookie_jwt(email: str, jwt_secret: str) -> str:
    """Generates an authentic HS256 signed session_token JWT matching Go backend schema."""
    now = int(datetime.datetime.now(datetime.timezone.utc).timestamp())
    header = _base64url_encode(b'{"alg":"HS256","typ":"JWT"}')
    payload_data = {
        "email": email,
        "name": email.split("@")[0],
        "picture": "",
        "iss": "xmanager-cloud-backend",
        "aud": ["xmanager-cloud-frontend"],
        "iat": now,
        "nbf": now,
        "exp": now + 3600,
    }
    payload = _base64url_encode(json.dumps(payload_data, separators=(",", ":")).encode())
    sig = _base64url_encode(
        hmac.new(jwt_secret.encode(), f"{header}.{payload}".encode(), hashlib.sha256).digest()
    )
    return f"{header}.{payload}.{sig}"


DEFAULT_USER_EMAIL: str = os.environ.get("XMC_USER_EMAIL", "janzak@google.com")
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
        jwt_secret: str | None = None,
        session_token: str | None = None,
        user_email: str | None = None,
        headless: bool = True,
        screenshots_dir: str | None = None,
    ) -> None:
        self.ui_base_url = ui_base_url.rstrip("/")
        self.auth_token = auth_token or os.environ.get("XMC_AUTH_TOKEN")
        self.jwt_secret = jwt_secret or os.environ.get("JWT_SECRET")
        self.session_token = session_token or os.environ.get("XMC_SESSION_TOKEN")
        self.user_email = user_email or os.environ.get("XMC_USER_EMAIL", DEFAULT_USER_EMAIL)
        self.headless = headless
        self.screenshots_dir = screenshots_dir or os.environ.get(
            "XMC_SCREENSHOTS_DIR", DEFAULT_SCREENSHOTS_DIR
        )
        p = urllib.parse.urlparse(self.ui_base_url)
        self.domain_host, self.scheme = p.hostname or "localhost", p.scheme or "http"
        self._playwright_mgr: Any = None
        self._browser: Any = None
        self._context: Any = None
        self._page: Any = None

    def __enter__(self) -> XMCUIBrowserSession:
        self.start()
        return self

    def __exit__(self, *args: Any) -> None:
        self.close()

    def start(self) -> None:
        """Launches Chromium and configures the authenticated browser context."""
        if sync_playwright is None:
            raise RuntimeError("Playwright is required. Install with `pip install playwright`.")
        os.makedirs(self.screenshots_dir, exist_ok=True)

        session_token_value = self.session_token
        if not session_token_value and self.jwt_secret:
            try:
                session_token_value = generate_session_cookie_jwt(self.user_email, self.jwt_secret)
            except Exception as err:
                raise UIVerificationError(
                    f"Failed to generate HS256 session_token JWT: {err}"
                ) from err

        print(
            f"\n[UI Session] Playwright Chromium (headless={self.headless}) -> {self.ui_base_url}"
        )
        self._playwright_mgr = sync_playwright().start()
        self._browser = self._playwright_mgr.chromium.launch(
            headless=self.headless,
            args=["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
        )
        self._context = self._browser.new_context(
            ignore_https_errors=True,
            viewport={"width": 1920, "height": 1080},
            device_scale_factor=2,
        )

        sec = self.scheme == "https"
        cookies = [
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
                ("session_token", session_token_value),
            )
            if v
        ]
        if cookies:
            self._context.add_cookies(cookies)

        self._page = self._context.new_page()
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

    def close(self) -> None:
        if self._browser:
            self._browser.close()
            self._browser = None
        if self._playwright_mgr:
            self._playwright_mgr.stop()
            self._playwright_mgr = None

    @property
    def page(self) -> Page:
        if self._page is None:
            raise RuntimeError("Browser session not started. Call start() or use context manager.")
        return self._page

    def settle_and_wait_fonts(self, timeout_ms: int = 500) -> None:
        try:
            self.page.evaluate("() => document.fonts.ready")
        except Exception:
            pass
        self.page.wait_for_timeout(timeout_ms)

    def take_screenshot(self, filename: str, full_page: bool = True) -> str:
        self.settle_and_wait_fonts()
        path = os.path.join(self.screenshots_dir, filename)
        self.page.screenshot(path=path, full_page=full_page)
        return path

    def navigate_to_experiment(self, xid: str | int, timeout_ms: int = 60000) -> None:
        target_url = f"{self.ui_base_url}/experiments/{xid}"
        print(f"\n[UI Action] Navigating to Experiment Dashboard: {target_url}...")
        self.page.goto(target_url, wait_until="networkidle", timeout=timeout_ms)
        self.settle_and_wait_fonts()
        shot = self.take_screenshot(f"ui_step1_landing_xid_{xid}.png")
        print(f"  [✓] Page loaded. URL: {self.page.url} -> Screenshot: {shot}")

        body = self.page.locator("body").inner_text() or ""
        if "Sign in with Google" in body or "Secure ML Experiment Management" in body:
            err_shot = self.take_screenshot(f"ui_AUTHENTICATION_REQUIRED_xid_{xid}.png")
            raise UIVerificationError(
                f"❌ AUTHENTICATION REQUIRED: Landed on Guest Login. Screenshot -> {err_shot}"
            )

    def assert_experiment_header(
        self, expected_title_pattern: str | None = None, timeout_ms: int = 30000
    ) -> str:
        print("\n[UI Assertion] Asserting Experiment Header rendering...")
        self.page.wait_for_selector(
            ".experiment-header-card, .xid-and-title, h1.experiment-title, xmc-experiment-page",
            timeout=timeout_ms,
        )
        self.settle_and_wait_fonts()
        text = (
            self.page.locator("h1.experiment-title, .experiment-title").first.text_content() or ""
        ).strip()
        if expected_title_pattern and expected_title_pattern not in text:
            raise UIVerificationError(
                f"Expected header title pattern '{expected_title_pattern}' not found in DOM text: '{text}'"
            )
        print(f"  [✓] Verified Header Title DOM text: '{text}'")
        return text
