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

"""Unit tests for xmc.auth.deployment_server."""

import unittest

from xmc.auth import AuthError, deployment_server

_EXAMPLE = "https://xmc.example.com"


class ResolveServerTest(unittest.TestCase):
    """Tests server precedence."""

    def test_precedence(self) -> None:
        """The flag beats XMC_API_URL, which beats XMANAGER_UI_URL."""
        env = {
            "XMC_API_URL": "https://api.example.com",
            "XMANAGER_UI_URL": "https://ui.example.com",
        }
        ui_only = {"XMC_API_URL": " ", "XMANAGER_UI_URL": "https://ui.example.com"}
        cases = [
            (_EXAMPLE, env, _EXAMPLE),
            (None, env, "https://api.example.com"),
            ("  ", ui_only, "https://ui.example.com"),
        ]
        for flag, case_env, want in cases:
            with self.subTest(flag=flag, env=case_env):
                self.assertEqual(deployment_server.resolve_server(flag, case_env), want)

    def test_nothing_configured(self) -> None:
        """A missing server raises a hint naming the flag and variables."""
        with self.assertRaisesRegex(AuthError, "--server"):
            deployment_server.resolve_server(None, {})

    def test_falls_back_to_xmc_domain(self) -> None:
        """XMC_DOMAIN is used when --server, XMC_API_URL, and XMANAGER_UI_URL are unset."""
        domain = {"XMC_DOMAIN": "xmc.example.com"}
        ui = {"XMANAGER_UI_URL": "https://ui.example.com", **domain}
        cases = [
            (None, domain, _EXAMPLE),
            (None, {"XMC_DOMAIN": " https://xmc.example.com/ "}, _EXAMPLE),
            (None, {"XMANAGER_UI_URL": " ", **domain}, _EXAMPLE),
            (None, ui, "https://ui.example.com"),
            ("https://flag.example.com", domain, "https://flag.example.com"),
        ]
        for flag, env, want in cases:
            with self.subTest(flag=flag, env=env):
                self.assertEqual(deployment_server.resolve_server(flag, env), want)

    def test_rejects_insecure_xmc_domain(self) -> None:
        """An http:// non-loopback XMC_DOMAIN is rejected."""
        with self.assertRaises(AuthError):
            deployment_server.resolve_server(None, {"XMC_DOMAIN": "http://xmc.example.com"})


class ParseServerTest(unittest.TestCase):
    """Tests URL normalization and HTTPS enforcement."""

    def test_normalizes_to_origin(self) -> None:
        """Paths, queries, trailing slashes, and host case are dropped; ports are kept."""
        cases = {
            "https://xmc.example.com/": _EXAMPLE,
            "https://XMC.Example.com/v1": _EXAMPLE,
            "https://xmc.example.com/v1/?x=1": _EXAMPLE,
            "https://xmc.example.com:8443": "https://xmc.example.com:8443",
            "http://localhost:8080/": "http://localhost:8080",
            "http://127.0.0.1:9000": "http://127.0.0.1:9000",
            "http://[::1]:9000": "http://[::1]:9000",
        }
        for raw, want in cases.items():
            with self.subTest(raw=raw):
                self.assertEqual(deployment_server.parse_server(raw), want)

    def test_rejects_invalid_or_insecure(self) -> None:
        """Malformed, credentialed, or non-loopback HTTP URLs are rejected."""
        for raw in (
            "http://xmc.example.com",
            "xmc.example.com",
            "ftp://xmc.example.com",
            "https://user:pw@xmc.example.com",
            "https://xmc.example.com:notaport",
            "https://",
        ):
            with self.subTest(raw=raw), self.assertRaises(AuthError):
                deployment_server.parse_server(raw)


class IsLoopbackHostTest(unittest.TestCase):
    """Tests loopback host detection."""

    def test_is_loopback_host(self) -> None:
        """Only localhost and loopback IPs count as loopback."""
        cases = {
            "localhost": True,
            "LOCALHOST": True,
            "127.0.0.1": True,
            "127.1.2.3": True,
            "::1": True,
            "10.0.0.1": False,
            "xmc.example.com": False,
            "localhost.example.com": False,
        }
        for host, want in cases.items():
            with self.subTest(host=host):
                self.assertEqual(deployment_server.is_loopback_host(host), want)


if __name__ == "__main__":
    unittest.main()
