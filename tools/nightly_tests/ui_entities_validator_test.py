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

import hashlib
import hmac
import json
import unittest
from unittest import mock

from tools.nightly_tests import ui_entities_validator


class UIEntitiesValidatorTest(unittest.TestCase):
    """Test suite for ui_entities_validator library."""

    def test_generate_session_cookie_jwt(self) -> None:
        secret = "test-secret-key-at-least-32-bytes-long!!"
        token = ui_entities_validator.generate_session_cookie_jwt("testuser@google.com", secret)
        parts = token.split(".")
        self.assertEqual(len(parts), 3)
        self.assertEqual(
            json.loads(ui_entities_validator._base64url_decode(parts[0])),
            {"alg": "HS256", "typ": "JWT"},
        )
        payload = json.loads(ui_entities_validator._base64url_decode(parts[1]))
        self.assertEqual(payload["email"], "testuser@google.com")
        self.assertEqual(payload["name"], "testuser")
        self.assertEqual(payload["iss"], "xmanager-cloud-backend")
        self.assertIn("xmanager-cloud-frontend", payload["aud"])
        self.assertGreater(payload["exp"], payload["iat"])

        sig = ui_entities_validator._base64url_encode(
            hmac.new(secret.encode(), f"{parts[0]}.{parts[1]}".encode(), hashlib.sha256).digest()
        )
        self.assertEqual(parts[2], sig)

    def test_ui_browser_session_init_and_defaults(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession(
            "https://xmc.example.com/", "tok", "sec", "stok", "u@google.com", True, "/tmp/s"
        )
        self.assertEqual(
            (session.ui_base_url, session.domain_host, session.scheme, session.user_email),
            ("https://xmc.example.com", "xmc.example.com", "https", "u@google.com"),
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

        mock_pw = mock.MagicMock()
        mock_browser, mock_ctx, mock_page = mock.MagicMock(), mock.MagicMock(), mock.MagicMock()
        mock_pw.chromium.launch.return_value = mock_browser
        mock_browser.new_context.return_value = mock_ctx
        mock_ctx.new_page.return_value = mock_page

        with mock.patch.object(
            ui_entities_validator,
            "sync_playwright",
            return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
        ):
            with session:
                self.assertEqual(session.page, mock_page)
            mock_browser.close.assert_called_once()
            mock_pw.stop.assert_called_once()

    def test_jwt_generation_failure_raises_verification_error(self) -> None:
        with (
            mock.patch.object(
                ui_entities_validator, "sync_playwright", return_value=mock.MagicMock()
            ),
            mock.patch.object(
                ui_entities_validator,
                "generate_session_cookie_jwt",
                side_effect=ValueError("bad key"),
            ),
        ):
            with self.assertRaises(ui_entities_validator.UIVerificationError):
                ui_entities_validator.XMCUIBrowserSession(
                    "https://xmc.example.com", jwt_secret="bad"
                ).start()

    def test_cookie_injection_modes(self) -> None:
        cases = [
            ("tok", None, None, ["GCP_IAAP_AUTH_TOKEN"]),
            (None, "sec", None, ["session_token"]),
            (None, None, "raw-session", ["session_token"]),
            ("tok", "sec", None, ["GCP_IAAP_AUTH_TOKEN", "session_token"]),
            (None, None, None, []),
        ]
        for auth, sec, sess, expected_names in cases:
            with self.subTest(auth=auth, sec=sec, sess=sess):
                mock_ctx = mock.MagicMock()
                mock_pw = mock.MagicMock()
                mock_pw.chromium.launch.return_value.new_context.return_value = mock_ctx
                with mock.patch.object(
                    ui_entities_validator,
                    "sync_playwright",
                    return_value=mock.MagicMock(start=mock.MagicMock(return_value=mock_pw)),
                ):
                    s = ui_entities_validator.XMCUIBrowserSession(
                        "https://xmc.example.com", auth, sec, sess
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
        mock_page = mock.MagicMock()
        mock_pw = mock.MagicMock()
        mock_pw.chromium.launch.return_value.new_context.return_value.new_page.return_value = (
            mock_page
        )

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
        mock_page = mock.MagicMock()
        session._page = mock_page
        mock_page.locator.return_value.inner_text.return_value = "Dashboard ready"
        mock_page.locator.return_value.first.text_content.return_value = (
            "  Demo Workload - Exp 10  "
        )

        session.navigate_to_experiment(10)
        mock_page.goto.assert_called_once_with(
            "https://xmc.example.com/experiments/10", wait_until="networkidle", timeout=60000
        )
        mock_page.screenshot.assert_called()

        header = session.assert_experiment_header(expected_title_pattern="Demo Workload")
        self.assertEqual(header, "Demo Workload - Exp 10")

        # Mismatch title
        with self.assertRaises(ui_entities_validator.UIVerificationError):
            session.assert_experiment_header(expected_title_pattern="Nonexistent")

        # Guest login redirection detection
        mock_page.locator.return_value.inner_text.return_value = "Sign in with Google"
        with self.assertRaises(ui_entities_validator.UIVerificationError):
            session.navigate_to_experiment(10)


if __name__ == "__main__":
    unittest.main()
