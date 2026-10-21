# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for xmc.auth.login_flow."""

from __future__ import annotations

import io
import re
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from unittest import mock

from xmc.auth import AuthError, login_flow
from xmc.auth.login_flow import LoginDependencies
from xmc.auth.token_client import TokenGrant

_SERVER = "https://xmc.example.com"
_GRANT = TokenGrant(access_token="tok-123", email="user@example.com", expires_in=43200)
_CODE = "c" * 43


class BrowserLikelyAvailableTest(unittest.TestCase):
    """Tests browser availability heuristics."""

    def test_cases(self) -> None:
        """SSH and display-less Unix sessions fall back to manual login."""
        cases = [
            ({"DISPLAY": ":0"}, "linux", True),
            ({"WAYLAND_DISPLAY": "wayland-0"}, "linux", True),
            ({}, "linux", False),
            ({"DISPLAY": ""}, "linux", False),
            ({}, "freebsd14", False),
            ({}, "darwin", True),
            ({}, "win32", True),
            ({}, "msys", True),
            ({"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, "linux", False),
            ({"SSH_TTY": "/dev/pts/1"}, "darwin", False),
        ]
        for env, platform, want in cases:
            with self.subTest(env=env, platform=platform):
                self.assertEqual(login_flow.browser_likely_available(env, platform), want)


class PkceTest(unittest.TestCase):
    """Tests PKCE verifier, challenge, and state generation."""

    def test_s256_challenge_matches_rfc7636_appendix_b(self) -> None:
        """The S256 transform matches the RFC 7636 Appendix B test vector."""
        verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
        self.assertEqual(
            login_flow.s256_challenge(verifier), "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
        )

    def test_new_verifier_is_rfc7636_compliant(self) -> None:
        """Verifiers use the unreserved alphabet within 43-128 chars and are random."""
        verifier = login_flow.new_verifier()
        self.assertRegex(verifier, r"^[A-Za-z0-9._~-]{43,128}$")
        self.assertEqual(len(login_flow.s256_challenge(verifier)), 43)
        self.assertNotEqual(verifier, login_flow.new_verifier())

    def test_new_state_matches_server_contract(self) -> None:
        """State values satisfy the server's 1-128 char [A-Za-z0-9_-] contract."""
        state = login_flow.new_state()
        self.assertRegex(state, r"^[A-Za-z0-9_-]{43,128}$")
        self.assertNotEqual(state, login_flow.new_state())


def _query(url: str) -> dict[str, str]:
    """Parses a URL's query into a flat dict."""
    return dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(url).query))


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args: object, **kwargs: object) -> None:
        return None


def _deliver_redirect(url: str) -> bool:
    """Plays a blocking browser: loads the loopback redirect, then returns."""
    params = _query(url)
    target = f"http://127.0.0.1:{params['port']}/callback?code={_CODE}&state={params['state']}"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect)
    try:
        opener.open(target, timeout=10)
    except urllib.error.HTTPError:
        pass  # The listener's 302 surfaces as HTTPError without redirects.
    return True


def _follow_redirect_to_loopback(url: str) -> bool:
    """Plays a background browser: delivers the redirect asynchronously."""
    threading.Thread(target=_deliver_redirect, args=(url,), daemon=True).start()
    return True


class BuildLoginUrlTest(unittest.TestCase):
    """Tests login URL construction against the HTTP contract."""

    def test_loopback_mode(self) -> None:
        """Loopback URLs carry port, state, challenge, S256, and login_hint."""
        url = login_flow.build_login_url(_SERVER, "st", "ch", port=5000, login_hint="u@x.com")
        self.assertTrue(url.startswith("https://xmc.example.com/api/cli/login?"))
        self.assertEqual(
            _query(url),
            {
                "port": "5000",
                "state": "st",
                "code_challenge": "ch",
                "code_challenge_method": "S256",
                "login_hint": "u@x.com",
            },
        )

    def test_manual_mode(self) -> None:
        """Manual URLs use mode=manual and omit port and empty hints."""
        params = _query(login_flow.build_login_url(_SERVER, "st", "ch"))
        self.assertEqual(params["mode"], "manual")
        self.assertNotIn("port", params)
        self.assertNotIn("login_hint", params)


class RunLoginTest(unittest.TestCase):
    """Tests the end-to-end login orchestration with fakes."""

    def setUp(self) -> None:
        self.out = io.StringIO()
        self.exchange = mock.Mock(return_value=_GRANT)
        self.read_code = mock.Mock(return_value=f"  {_CODE}\n")
        self.open_browser = mock.Mock(side_effect=_follow_redirect_to_loopback)

    def _deps(self) -> LoginDependencies:
        return LoginDependencies(self.out, self.open_browser, self.read_code, self.exchange)

    def _assert_exchanged_with_matching_verifier(self, login_url: str) -> None:
        server, code, verifier = self.exchange.call_args.args
        self.assertEqual((server, code), (_SERVER, _CODE))
        self.assertEqual(login_flow.s256_challenge(verifier), _query(login_url)["code_challenge"])

    def test_browser_flow(self) -> None:
        """The loopback flow opens the browser, receives the code, and exchanges it."""
        grant = login_flow.run_login(_SERVER, self._deps(), use_browser=True, timeout=10)
        self.assertEqual(grant, _GRANT)
        login_url = self.open_browser.call_args.args[0]
        self.assertIn(login_url, self.out.getvalue())
        self._assert_exchanged_with_matching_verifier(login_url)
        self.read_code.assert_not_called()

    def test_browser_launch_error_falls_back_to_manual(self) -> None:
        """A browser launcher that raises is treated as unavailable."""
        self.open_browser.side_effect = RuntimeError("bad BROWSER")
        login_flow.run_login(_SERVER, self._deps(), use_browser=True)
        self.read_code.assert_called_once()

    def test_falls_back_to_manual_when_browser_fails(self) -> None:
        """If the browser cannot be opened, a fresh manual attempt runs instead."""
        self.open_browser.side_effect = None
        self.open_browser.return_value = False
        login_flow.run_login(_SERVER, self._deps(), use_browser=True, login_hint="u@x.com")
        self.read_code.assert_called_once_with(login_flow.MANUAL_CODE_PROMPT)
        browser_url = self.open_browser.call_args.args[0]
        manual_url = re.findall(r"https://\S+", self.out.getvalue())[-1]
        self.assertEqual(_query(manual_url)["mode"], "manual")
        self.assertEqual(_query(manual_url)["login_hint"], "u@x.com")
        for param in ("state", "code_challenge"):
            self.assertNotEqual(_query(manual_url)[param], _query(browser_url)[param])
        self._assert_exchanged_with_matching_verifier(manual_url)

    def test_manual_flow_warns_and_strips_code(self) -> None:
        """Manual mode prints the anti-phishing warning and trims pasted input."""
        login_flow.run_login(_SERVER, self._deps(), use_browser=False)
        self.open_browser.assert_not_called()
        self.assertIn(login_flow.MANUAL_WARNING, self.out.getvalue())
        self.assertEqual(self.exchange.call_args.args[1], _CODE)

    def test_manual_flow_rejects_invalid_code(self) -> None:
        """Empty or malformed pasted codes fail without calling the server."""
        for pasted in ("", "   ", "not a code!"):
            with self.subTest(pasted=pasted):
                self.read_code.return_value = pasted
                with self.assertRaisesRegex(AuthError, "No valid login code"):
                    login_flow.run_login(_SERVER, self._deps(), use_browser=False)
        self.exchange.assert_not_called()


if __name__ == "__main__":
    unittest.main()
