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

"""Unit tests for ui_entities_validator module."""

from __future__ import annotations

import os
import pathlib
import unittest
from typing import Any
from unittest import mock

from tools.nightly_tests import ui_entities_validator


class UIEntitiesValidatorTest(unittest.TestCase):
    """Test suite for ui_entities_validator library."""

    def setUp(self) -> None:
        super().setUp()
        self.enterContext(mock.patch.object(os, "makedirs"))

    def _create_mock_playwright(self) -> tuple[Any, Any, Any, Any]:
        """Creates a mocked Playwright hierarchy with default experiment page state."""
        mock_pw = mock.MagicMock()
        mock_browser = mock.MagicMock()
        mock_ctx = mock.MagicMock()
        mock_page = mock.MagicMock()
        mock_page.url = "https://xmc.example.com/experiments/10"
        mock_page.locator.return_value.count.return_value = 0
        mock_pw.chromium.launch.return_value = mock_browser
        mock_browser.new_context.return_value = mock_ctx
        mock_ctx.new_page.return_value = mock_page
        return mock_pw, mock_browser, mock_ctx, mock_page

    def test_ui_browser_session_init_and_defaults(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc.example.com/",
            auth_token="tok",
            session_token="stok",
            user_email="u@google.com",
            user_password="pw",
            dex_url="https://dex.example.com",
            headless=True,
            screenshots_dir="/tmp/s",
        )
        self.assertEqual(
            (
                session.ui_base_url,
                session.domain_host,
                session.scheme,
                session.user_email,
                session.dex_url,
            ),
            (
                "https://xmc.example.com",
                "xmc.example.com",
                "https",
                "u@google.com",
                "https://dex.example.com",
            ),
        )
        with mock.patch.dict("os.environ", {}, clear=True):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            self.assertEqual(
                (s.user_email, s.screenshots_dir), ("test@example.com", "/tmp/xmc_screenshots")
            )

    def test_ui_verification_error(self) -> None:
        self.assertIsInstance(ui_entities_validator.UIVerificationError("msg"), AssertionError)

    def test_session_lifecycle_and_page_property(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        with self.assertRaises(RuntimeError):
            _ = session.page

        mock_pw, mock_browser, mock_ctx, mock_page = self._create_mock_playwright()

        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            with session:
                self.assertEqual(session.page, mock_page)
            mock_ctx.close.assert_called_once()
            mock_browser.close.assert_called_once()
            mock_pw.stop.assert_called_once()

    def test_dex_authenticator_session_cookie_injection(self) -> None:
        mock_pw, _, _, mock_page = self._create_mock_playwright()
        with (
            mock.patch.object(
                ui_entities_validator,
                "sync_playwright",
                return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
            ),
            mock.patch.object(ui_entities_validator, "DexAuthenticator") as mock_dex_cls,
        ):
            session = ui_entities_validator.XMCUIBrowserSession(
                ui_base_url="https://xmc.example.com",
                user_email="admin@xmc.local",
                user_password="secret-password",
                dex_url="https://dex.example.com",
                screenshots_dir="/tmp/shots",
            )
            session.start()

            mock_dex_cls.assert_called_once_with(
                base_url="https://xmc.example.com",
                dex_url="https://dex.example.com",
            )
            mock_dex_cls.return_value.authenticate.assert_called_once_with(
                mock_page,
                "admin@xmc.local",
                "secret-password",
                pathlib.Path("/tmp/shots"),
            )
            session.close()

    def test_start_cleans_up_resources_on_launch_failure(self) -> None:
        mock_pw, _, _, _ = self._create_mock_playwright()
        mock_pw.chromium.launch.side_effect = RuntimeError("chromium binary missing")

        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            with self.assertRaisesRegex(RuntimeError, "chromium binary missing"):
                session.start()

            mock_pw.stop.assert_called_once()
            self.assertIsNone(session._playwright)
            self.assertIsNone(session._browser)
            self.assertIsNone(session._context)

    def test_close_continues_cleanup_when_context_close_raises(self) -> None:
        mock_pw, mock_browser, mock_ctx, _ = self._create_mock_playwright()
        mock_ctx.close.side_effect = RuntimeError("context already closed")
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session._playwright = mock_pw
        session._browser = mock_browser
        session._context = mock_ctx

        with self.assertRaisesRegex(RuntimeError, "context already closed"):
            session.close()

        mock_browser.close.assert_called_once()
        mock_pw.stop.assert_called_once()
        self.assertIsNone(session._context)
        self.assertIsNone(session._browser)
        self.assertIsNone(session._playwright)

    def test_cookie_injection_modes(self) -> None:
        cases = [
            ("tok", None, ["GCP_IAAP_AUTH_TOKEN"]),
            (None, "raw-session", ["session_token"]),
            ("tok", "raw-session", ["GCP_IAAP_AUTH_TOKEN", "session_token"]),
            (None, None, []),
        ]
        for auth, sess, expected_names in cases:
            with self.subTest(auth=auth, sess=sess):
                mock_pw, _, mock_ctx, _ = self._create_mock_playwright()
                with mock.patch.object(
                    ui_entities_validator,
                    "sync_playwright",
                    return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
                ):
                    s = ui_entities_validator.XMCUIBrowserSession(
                        "https://xmc.example.com",
                        auth_token=auth,
                        session_token=sess,
                    )
                    s.start()
                    if expected_names:
                        mock_ctx.add_cookies.assert_called_once()
                        cookies = mock_ctx.add_cookies.call_args[0][0]
                        self.assertEqual([c["name"] for c in cookies], expected_names)
                    else:
                        mock_ctx.add_cookies.assert_not_called()

    def test_route_interception_security(self) -> None:
        routes: list[tuple[str, bool]] = [
            ("https://xmc.example.com/api/v1/data", True),
            ("https://xmc.example.com.evil.com/api", False),
            ("https://cdn.org?url=xmc.example.com", False),
            ("https://fonts.googleapis.com/css", False),
        ]
        mock_pw, _, _, mock_page = self._create_mock_playwright()

        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            s = ui_entities_validator.XMCUIBrowserSession(
                "https://xmc.example.com", auth_token="bearer-tok"
            )
            s.start()

        handler = mock_page.route.call_args[0][1]
        for url, should_auth in routes:
            with self.subTest(url=url, should_auth=should_auth):
                route = mock.MagicMock(request=mock.MagicMock(url=url, headers={}))
                handler(route)
                if should_auth:
                    route.continue_.assert_called_once_with(
                        headers={"authorization": "Bearer bearer-tok"}
                    )
                else:
                    route.continue_.assert_called_once_with()

    def test_settle_and_wait_fonts_error_tolerance(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session._page = mock.MagicMock(
            evaluate=mock.MagicMock(side_effect=RuntimeError("context lost"))
        )
        session.settle_and_wait_fonts(timeout_ms=100)
        session._page.wait_for_timeout.assert_called_with(100)

    def test_navigation_and_assertions(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            "https://xmc.example.com", screenshots_dir="/tmp/shots"
        )
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page
        mock_page.locator.return_value.inner_text.return_value = "Dashboard ready"
        mock_page.locator.return_value.first.text_content.return_value = (
            "  Demo Workload - Exp 10  "
        )

        session.navigate_to_experiment(10)
        mock_page.goto.assert_called_once_with(
            "https://xmc.example.com/experiments/10", wait_until="networkidle", timeout=60000
        )
        mock_page.wait_for_selector.assert_called_once_with(
            "xmc-experiment-page, xmc-home-page, .guest-page-container",
            timeout=60000,
        )
        mock_page.screenshot.assert_called()

        header = session.assert_experiment_header(expected_title_pattern="Demo Workload")
        self.assertEqual(header, "Demo Workload - Exp 10")
        mock_page.locator.return_value.first.wait_for.assert_called_once_with(
            state="visible", timeout=30000
        )

        # Mismatch title
        with self.assertRaises(ui_entities_validator.UIVerificationError):
            session.assert_experiment_header(expected_title_pattern="Nonexistent")

        # Guest login / HTTP 401/403 redirection detection
        for bad_body in (
            "Sign in with Google",
            "HTTP ERROR 401",
            "HTTP ERROR 403",
            "Access Denied",
        ):
            mock_page.locator.return_value.inner_text.return_value = bad_body
            with self.assertRaises(ui_entities_validator.UIVerificationError):
                session.navigate_to_experiment(10)

    def test_assert_experiment_header_empty_text_raises(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page
        mock_page.locator.return_value.first.text_content.return_value = "   "

        with self.assertRaises(ui_entities_validator.UIVerificationError):
            session.assert_experiment_header(expected_title_pattern="Demo Workload")

    def test_switch_tab_success(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc-nightly.example.com"
        )
        mock_page = mock.MagicMock()
        session._page = mock_page
        mock_tab = mock.MagicMock()
        mock_page.locator.return_value.first = mock_tab

        session.switch_tab("Work Units", timeout_ms=5000)

        mock_tab.wait_for.assert_called_once_with(state="visible", timeout=5000)
        mock_tab.click.assert_called_once()

    def test_assert_work_units_tab_success(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc-nightly.example.com"
        )
        mock_page = mock.MagicMock()
        session._page = mock_page

        row1 = mock.MagicMock()
        row1.inner_text.return_value = "1 COMPLETED demo-worker-1"

        row2 = mock.MagicMock()
        row2.inner_text.return_value = "2 COMPLETED demo-worker-2"

        row3 = mock.MagicMock()
        row3.inner_text.return_value = "3 COMPLETED demo-worker-3"

        mock_page.locator.return_value.all.return_value = [row1, row2, row3]

        res = session.assert_work_units_tab(
            min_units=3,
            expected_names=["demo-worker-1", r"demo-worker-\d", "3"],
        )
        self.assertEqual(len(res), 3)

    def test_assert_work_units_tab_pattern_mismatch(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc-nightly.example.com"
        )
        mock_page = mock.MagicMock()
        session._page = mock_page

        row1 = mock.MagicMock()
        row1.inner_text.return_value = "1 COMPLETED demo-worker-1"

        mock_page.locator.return_value.all.return_value = [row1]

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_work_units_tab(
                min_units=1,
                expected_names=["missing-worker-name-99"],
            )
        self.assertIn("missing-worker-name-99", str(ctx.exception))

    def test_assert_work_units_tab_min_units_error(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc-nightly.example.com"
        )
        mock_page = mock.MagicMock()
        session._page = mock_page

        row1 = mock.MagicMock()
        row1.inner_text.return_value = "1 COMPLETED"

        mock_page.locator.return_value.all.return_value = [row1]

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_work_units_tab(min_units=3)
        self.assertIn("Expected at least 3 work unit row(s)", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
