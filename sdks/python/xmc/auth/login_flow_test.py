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
import threading
import unittest
import urllib.error
import urllib.parse
import urllib.request
from unittest import mock

from xmc.auth import login_flow
from xmc.auth.login_flow import LoginDependencies
from xmc.auth.token_client import TokenGrant

_SERVER = "https://xmc.example.com"
_GRANT = TokenGrant(access_token="tok-123", email="user@example.com", expires_in=43200)
_CODE = "c" * 43


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

    def test_omits_empty_login_hint(self) -> None:
        """No login_hint is sent without a cached email."""
        self.assertNotIn("login_hint", _query(login_flow.build_login_url(_SERVER, "s", "c", 1)))


class RunLoginTest(unittest.TestCase):
    """Tests the end-to-end login orchestration with fakes."""

    def setUp(self) -> None:
        self.out = io.StringIO()
        self.exchange = mock.Mock(return_value=_GRANT)
        self.open_browser = mock.Mock(side_effect=_follow_redirect_to_loopback)

    def _run(self) -> TokenGrant:
        deps = LoginDependencies(self.out, self.open_browser, self.exchange)
        return login_flow.run_login(_SERVER, deps, timeout=10, login_hint="u@x.com")

    def _assert_exchanged_with_matching_verifier(self) -> None:
        login_url = self.open_browser.call_args.args[0]
        self.assertIn(login_url, self.out.getvalue())
        self.assertEqual(_query(login_url)["login_hint"], "u@x.com")
        server, code, verifier = self.exchange.call_args.args
        self.assertEqual((server, code), (_SERVER, _CODE))
        self.assertEqual(login_flow.s256_challenge(verifier), _query(login_url)["code_challenge"])

    def test_browser_flow(self) -> None:
        """The loopback flow opens the browser, receives the code, and exchanges it."""
        self.assertEqual(self._run(), _GRANT)
        self._assert_exchanged_with_matching_verifier()

    def test_blocking_browser_does_not_deadlock(self) -> None:
        """A browser command that blocks on the loopback redirect still completes."""
        self.open_browser.side_effect = _deliver_redirect
        self.assertEqual(self._run(), _GRANT)
        self._assert_exchanged_with_matching_verifier()


if __name__ == "__main__":
    unittest.main()
