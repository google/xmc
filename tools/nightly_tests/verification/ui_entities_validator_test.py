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

import io
import os
import unittest
from typing import Any
from unittest import mock

from tools.nightly_tests.verification import ui_entities_validator


class UIEntitiesValidatorTest(unittest.TestCase):
    """Test suite for ui_entities_validator library."""

    def setUp(self) -> None:
        super().setUp()
        self.enterContext(mock.patch.object(os, "makedirs"))
        self.enterContext(
            mock.patch.object(
                ui_entities_validator.xdash_auth, "load_cached_cli_token", return_value=None
            )
        )

    def _create_mock_playwright(self) -> tuple[Any, Any, Any, Any]:
        """Creates a mocked Playwright hierarchy with selector-aware mock page state."""
        mock_pw = mock.MagicMock()
        mock_browser = mock.MagicMock()
        mock_ctx = mock.MagicMock()
        mock_page = mock.MagicMock()

        mock_page.url = "https://xmc.example.com/experiments/10"

        def _locator_factory(selector: str) -> Any:
            loc = mock.MagicMock()
            if "xmc-home-page" in selector or '[data-testid="guest-page-container"]' in selector:
                loc.count.return_value = 0
            elif "body" in selector:
                loc.inner_text.return_value = "Normal dashboard body content"
            elif '[data-testid="experiment-title"]' in selector:
                loc.first.text_content.return_value = "Demo Workload - Exp 10"
            return loc

        mock_page.locator.side_effect = _locator_factory
        mock_pw.chromium.launch.return_value = mock_browser
        mock_browser.new_context.return_value = mock_ctx
        mock_ctx.new_page.return_value = mock_page
        return mock_pw, mock_browser, mock_ctx, mock_page

    def test_ui_browser_session_init_and_defaults(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc.example.com/",
            auth_token="tok",
            session_token="stok",
            allow_insecure_tls=True,
            headless=True,
            screenshots_dir="/tmp/s",
        )
        self.assertEqual(
            (
                session.ui_base_url,
                session.domain_host,
                session.scheme,
                session.port,
                session.allow_insecure_tls,
            ),
            (
                "https://xmc.example.com",
                "xmc.example.com",
                "https",
                None,
                True,
            ),
        )
        with mock.patch.dict("os.environ", {}, clear=True):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            self.assertEqual(
                (s.screenshots_dir, s.allow_insecure_tls),
                ("/tmp/xmc_screenshots", False),
            )

    def test_session_init_respects_runtime_env(self) -> None:
        with mock.patch.dict(
            "os.environ",
            {
                "XMC_AUTH_TOKEN": "runtime-tok",
                "XMC_SESSION_TOKEN": "runtime-session-tok",
                "XMC_SCREENSHOTS_DIR": "/custom/screenshots",
            },
        ):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            self.assertEqual(s.auth_token, "runtime-tok")
            self.assertEqual(s.session_token, "runtime-session-tok")
            self.assertEqual(s.screenshots_dir, "/custom/screenshots")

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
            self.assertIsNone(session._page)

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
            self.assertIsNone(session._page)

    def test_close_swallows_exceptions_and_resets_state(self) -> None:
        mock_pw, mock_browser, mock_ctx, mock_page = self._create_mock_playwright()
        mock_ctx.close.side_effect = RuntimeError("context already closed")
        mock_browser.close.side_effect = RuntimeError("browser crash")
        mock_pw.stop.side_effect = RuntimeError("driver hung")

        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session._playwright = mock_pw
        session._browser = mock_browser
        session._context = mock_ctx
        session._page = mock_page

        # Should not raise any exception despite all close calls failing
        session.close()

        mock_ctx.close.assert_called_once()
        mock_browser.close.assert_called_once()
        mock_pw.stop.assert_called_once()
        self.assertIsNone(session._page)
        self.assertIsNone(session._context)
        self.assertIsNone(session._browser)
        self.assertIsNone(session._playwright)

    def test_allow_insecure_tls_flag(self) -> None:
        for insecure in (False, True):
            with self.subTest(insecure=insecure):
                mock_pw, mock_browser, _, _ = self._create_mock_playwright()
                with mock.patch.object(
                    ui_entities_validator,
                    "sync_playwright",
                    return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
                ):
                    s = ui_entities_validator.XMCUIBrowserSession(
                        "https://xmc.example.com", allow_insecure_tls=insecure
                    )
                    s.start()
                    mock_browser.new_context.assert_called_once()
                    self.assertEqual(
                        mock_browser.new_context.call_args[1]["ignore_https_errors"],
                        insecure,
                    )
                    s.close()

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
                    s.close()

    def test_route_interception_security(self) -> None:
        routes: list[tuple[str, bool]] = [
            ("https://xmc.example.com/api/v1/data", True),
            ("https://xmc.example.com:443/api/v1/data", True),  # default port normalized
            ("http://xmc.example.com/api/v1/data", False),  # cleartext downgrade prevented
            ("https://xmc.example.com:8443/api/v1/data", False),  # port mismatch
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
        s.close()

        # Verify base URL with explicit default port 443 matches implicit port in requests
        mock_pw2, _, _, mock_page2 = self._create_mock_playwright()
        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw2)),
        ):
            s2 = ui_entities_validator.XMCUIBrowserSession(
                "https://xmc.example.com:443", auth_token="bearer-tok"
            )
            s2.start()

        handler2 = mock_page2.route.call_args[0][1]
        route2 = mock.MagicMock(
            request=mock.MagicMock(url="https://xmc.example.com/api/v1/data", headers={})
        )
        handler2(route2)
        route2.continue_.assert_called_once_with(headers={"authorization": "Bearer bearer-tok"})
        s2.close()

    def test_response_logging_strips_query_and_fragment(self) -> None:
        mock_pw, _, _, mock_page = self._create_mock_playwright()

        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            s.start()

        # Find response listener
        response_listener = None
        for call in mock_page.on.call_args_list:
            if call[0][0] == "response":
                response_listener = call[0][1]
                break
        self.assertIsNotNone(response_listener)
        assert callable(response_listener)

        mock_resp = mock.MagicMock()
        mock_resp.status = 500
        mock_resp.url = "https://xmc.example.com/api/v1/resource?token=secret123#anchor"

        buf = io.StringIO()
        with mock.patch("sys.stdout", buf):
            response_listener(mock_resp)

        output = buf.getvalue()
        self.assertIn("https://xmc.example.com/api/v1/resource", output)
        self.assertNotIn("secret123", output)
        self.assertNotIn("anchor", output)
        s.close()

    def test_settle_and_wait_fonts_error_tolerance(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session._page = mock.MagicMock(
            evaluate=mock.MagicMock(side_effect=RuntimeError("context lost"))
        )
        session.settle_and_wait_fonts(timeout_ms=100)
        session._page.wait_for_timeout.assert_called_with(100)

    def test_wait_for_selector_with_retry_handles_timeout_and_http_errors(self) -> None:
        clean = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        clean._page = mock.MagicMock()
        clean.wait_for_selector_with_retry("xmc-queue-card", 5000)
        clean._page.wait_for_selector.assert_called_once_with("xmc-queue-card", timeout=5000)
        clean._page.reload.assert_not_called()

        for http_errs, page_errs in (
            (["500 https://xmc.example.com/v1/clusterqueues"], []),
            ([], ["TypeError: null"]),
            (["500 https://xmc.example.com/v1/clusterqueues"], ["TypeError: null"]),
        ):
            with self.subTest(http_errs=http_errs, page_errs=page_errs):
                session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
                mock_page = mock.MagicMock()
                session._page = mock_page
                session.http_errors = list(http_errs)
                session.page_errors = list(page_errs)
                session.wait_for_selector_with_retry("xmc-queue-card", 5000)
                self.assertEqual(session.http_errors, [])
                self.assertEqual(session.page_errors, [])
                mock_page.reload.assert_called_once_with(wait_until="networkidle", timeout=5000)
                self.assertEqual(mock_page.wait_for_selector.call_count, 2)

        session2 = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        mock_page2 = mock.MagicMock()
        session2._page = mock_page2
        mock_page2.wait_for_selector.side_effect = [RuntimeError("timeout"), None]
        session2.wait_for_selector_with_retry("xmc-queue-card", 5000)
        mock_page2.reload.assert_called_once_with(wait_until="networkidle", timeout=5000)
        self.assertEqual(mock_page2.wait_for_selector.call_count, 2)

        session3 = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        mock_page3 = mock.MagicMock()
        session3._page = mock_page3
        mock_page3.wait_for_selector.side_effect = [RuntimeError("t1"), RuntimeError("t2")]
        with self.assertRaisesRegex(RuntimeError, "t2"):
            session3.wait_for_selector_with_retry("xmc-queue-card", 5000)
        self.assertEqual(mock_page3.wait_for_selector.call_count, 2)

    def test_navigate_to_experiment_success(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            "https://xmc.example.com", screenshots_dir="/tmp/shots"
        )
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page

        session.navigate_to_experiment(10)
        mock_page.goto.assert_called_once_with(
            "https://xmc.example.com/experiments/10", wait_until="networkidle", timeout=60000
        )
        mock_page.wait_for_selector.assert_called_once_with(
            'xmc-experiment-page, xmc-home-page, [data-testid="guest-page-container"]',
            timeout=60000,
        )
        mock_page.screenshot.assert_called()

    def test_navigate_raises_on_auth_markers(self) -> None:
        for bad_body in (
            "Sign in with Google",
            "Secure ML Experiment Management",
            "HTTP ERROR 401",
            "HTTP ERROR 403",
            "Access Denied",
        ):
            with self.subTest(bad_body=bad_body):
                session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
                _, _, _, mock_page = self._create_mock_playwright()
                session._page = mock_page

                def _locator_factory(selector: str, body_text: str = bad_body) -> Any:
                    loc = mock.MagicMock()
                    if (
                        "xmc-home-page" in selector
                        or '[data-testid="guest-page-container"]' in selector
                    ):
                        loc.count.return_value = 0
                    elif "body" in selector:
                        loc.inner_text.return_value = f"Unauthorized: {body_text}"
                    return loc

                mock_page.locator.side_effect = _locator_factory

                with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
                    session.navigate_to_experiment(10)
                self.assertIn("AUTHENTICATION REQUIRED", str(ctx.exception))
                self.assertIn("ui_AUTHENTICATION_REQUIRED_xid_10.png", str(ctx.exception))

    def test_navigate_raises_on_guest_page_container(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page

        def _locator_factory(selector: str) -> Any:
            loc = mock.MagicMock()
            if "xmc-home-page" in selector or '[data-testid="guest-page-container"]' in selector:
                loc.count.return_value = 1  # Landed on guest page
            elif "body" in selector:
                loc.inner_text.return_value = "Normal text"
            return loc

        mock_page.locator.side_effect = _locator_factory

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.navigate_to_experiment(10)
        self.assertIn("AUTHENTICATION REQUIRED", str(ctx.exception))
        self.assertIn("ui_AUTHENTICATION_REQUIRED_xid_10.png", str(ctx.exception))

    def test_navigate_raises_on_unexpected_redirect(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page
        mock_page.url = "https://xmc.example.com/unexpected/redirect"

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.navigate_to_experiment(10)
        self.assertIn("UNEXPECTED REDIRECT", str(ctx.exception))
        self.assertIn("ui_REDIRECT_xid_10.png", str(ctx.exception))
        self.assertIn("/experiments/10", str(ctx.exception))
        self.assertIn("/unexpected/redirect", str(ctx.exception))

    def test_navigate_supports_base_url_path_prefix(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com/subpath/")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page
        mock_page.url = "https://xmc.example.com/subpath/experiments/10"

        # Should succeed because expected_path includes '/subpath'
        session.navigate_to_experiment(10)
        mock_page.goto.assert_called_once_with(
            "https://xmc.example.com/subpath/experiments/10",
            wait_until="networkidle",
            timeout=60000,
        )

    def test_assert_experiment_header_success(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        mock_page = mock.MagicMock()
        mock_title = mock.MagicMock()
        mock_title.first.text_content.return_value = "Demo Workload - Exp 10"
        mock_page.locator.return_value = mock_title
        session._page = mock_page

        header = session.assert_experiment_header(expected_title_pattern="Demo Workload")
        self.assertEqual(header, "Demo Workload - Exp 10")
        mock_title.first.wait_for.assert_called_once_with(state="visible", timeout=30000)

    def test_assert_experiment_header_empty_text_raises(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page

        def _locator_factory(selector: str) -> Any:
            loc = mock.MagicMock()
            loc.first.text_content.return_value = "   "
            return loc

        mock_page.locator.side_effect = _locator_factory

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_experiment_header(expected_title_pattern="Demo Workload")
        self.assertIn("empty text", str(ctx.exception))

    def test_assert_experiment_header_pattern_mismatch_raises(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_experiment_header(expected_title_pattern="NonexistentPattern")
        self.assertIn("NonexistentPattern", str(ctx.exception))

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

        mock_page.locator.return_value.all_inner_texts.return_value = [
            "1 COMPLETED demo-worker-1",
            "2 COMPLETED demo-worker-2",
            "3 COMPLETED demo-worker-3",
        ]

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

        mock_page.locator.return_value.all_inner_texts.return_value = ["1 COMPLETED demo-worker-1"]

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

        mock_page.locator.return_value.all_inner_texts.return_value = ["1 COMPLETED"]

        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_work_units_tab(min_units=3)
        self.assertIn("Expected at least 3 work unit row(s)", str(ctx.exception))

    def test_assert_work_units_tab_invalid_regex_fallback_literal_match(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            ui_base_url="https://xmc-nightly.example.com"
        )
        mock_page = mock.MagicMock()
        session._page = mock_page

        mock_page.locator.return_value.all_inner_texts.return_value = [
            "1 COMPLETED demo-[worker-1",
        ]

        res = session.assert_work_units_tab(
            min_units=1,
            expected_names=["demo-[worker-1"],
        )
        self.assertEqual(len(res), 1)

    def test_pageerror_and_same_origin_http_errors_captured(self) -> None:
        mock_pw, _, _, mock_page = self._create_mock_playwright()
        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
            s.start()

        listeners = {call[0][0]: call[0][1] for call in mock_page.on.call_args_list}
        listeners["pageerror"](RuntimeError("uncaught TypeError"))
        self.assertEqual(s.page_errors, ["uncaught TypeError"])

        with mock.patch("sys.stdout", io.StringIO()):
            listeners["response"](
                mock.MagicMock(status=502, url="https://xmc.example.com/v1/clusterqueues?a=1")
            )
            listeners["response"](
                mock.MagicMock(status=404, url="https://external-cdn.example.org/font.woff2")
            )
            listeners["response"](
                mock.MagicMock(status=200, url="https://xmc.example.com/api/v1/ok")
            )

        self.assertEqual(s.http_errors, ["502 https://xmc.example.com/v1/clusterqueues"])
        s.close()

    def test_navigate_to_route_success_and_error_cases(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page
        mock_page.url = "https://xmc.example.com/resources"

        session.navigate_to_route("resources", "xmc-resource-dashboard", "rod_landing.png")
        mock_page.goto.assert_called_once_with(
            "https://xmc.example.com/resources", wait_until="networkidle", timeout=60000
        )
        mock_page.wait_for_selector.assert_called_once_with(
            'xmc-resource-dashboard, xmc-home-page, [data-testid="guest-page-container"]',
            timeout=60000,
        )

        mock_page.url = "https://xmc.example.com/unexpected"
        with self.assertRaises(ui_entities_validator.UIVerificationError) as redir_ctx:
            session.navigate_to_route("resources", "xmc-resource-dashboard", "rod_landing.png")
        self.assertIn("UNEXPECTED REDIRECT", str(redir_ctx.exception))

        mock_page.url = "https://xmc.example.com/resources"
        mock_page.locator.side_effect = lambda sel: mock.MagicMock(
            count=mock.MagicMock(return_value=1),
            inner_text=mock.MagicMock(return_value="Access Denied"),
        )
        with self.assertRaises(ui_entities_validator.UIVerificationError) as auth_ctx:
            session.navigate_to_route("resources", "xmc-resource-dashboard", "rod_landing.png")
        self.assertIn("AUTHENTICATION REQUIRED", str(auth_ctx.exception))

    def test_assert_no_runtime_errors_pass_and_failures(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        _, _, _, mock_page = self._create_mock_playwright()
        session._page = mock_page

        def _clean_locator(selector: str) -> Any:
            loc = mock.MagicMock()
            loc.count.return_value = 0
            return loc

        mock_page.locator.side_effect = _clean_locator
        session.assert_no_runtime_errors("clean_check")

        session.page_errors.append("TypeError: null")
        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_no_runtime_errors("page_err")
        self.assertIn("TypeError: null", str(ctx.exception))
        session.page_errors.clear()

        session.http_errors.append("500 https://xmc.example.com/v1/clusterqueues")
        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_no_runtime_errors("http_err")
        self.assertIn("500 https://xmc.example.com/v1/clusterqueues", str(ctx.exception))
        session.http_errors.clear()

        def _banner_locator(selector: str) -> Any:
            loc = mock.MagicMock()
            loc.count.return_value = 1
            loc.first.text_content.return_value = "Backend unreachable"
            return loc

        mock_page.locator.side_effect = _banner_locator
        with self.assertRaises(ui_entities_validator.UIVerificationError) as ctx:
            session.assert_no_runtime_errors("banner_err")
        self.assertIn("Backend unreachable", str(ctx.exception))

    def test_parse_numeric_xid_and_from_flags(self) -> None:
        self.assertEqual(ui_entities_validator.parse_numeric_xid("experiments/42"), "42")
        with self.assertRaises(ui_entities_validator.UIVerificationError):
            ui_entities_validator.parse_numeric_xid("invalid-xid")
        fake_flags = mock.MagicMock(
            ui_base_url="https://xmc.example.com",
            auth_token="tok-1",
            headless=False,
            screenshots_dir="/tmp/s",
        )
        s = ui_entities_validator.XMCUIBrowserSession.from_flags(fake_flags)
        self.assertEqual(s.ui_base_url, "https://xmc.example.com")
        self.assertEqual(s.auth_token, "tok-1")
        self.assertFalse(s.headless)
        self.assertEqual(s.screenshots_dir, "/tmp/s")

    def test_session_init_falls_back_to_cached_cli_token(self) -> None:
        with (
            mock.patch.dict("os.environ", {}, clear=True),
            mock.patch.object(
                ui_entities_validator.xdash_auth,
                "load_cached_cli_token",
                return_value="cli-browser-tok",
            ) as mock_cli_tok,
        ):
            s = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com/")
        mock_cli_tok.assert_called_once_with("https://xmc.example.com")
        self.assertEqual(s.auth_token, "cli-browser-tok")

        # Schemeless ui_base_url does not trigger CLI token auto-discovery
        with (
            mock.patch.dict("os.environ", {}, clear=True),
            mock.patch.object(
                ui_entities_validator.xdash_auth,
                "load_cached_cli_token",
                return_value="cli-browser-tok",
            ) as mock_cli_tok_schemeless,
        ):
            s_no_scheme = ui_entities_validator.XMCUIBrowserSession("xmc.example.com")
        mock_cli_tok_schemeless.assert_not_called()
        self.assertIsNone(s_no_scheme.auth_token)


if __name__ == "__main__":
    unittest.main()
