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

"""Unit tests for xmc.auth.pkce."""

import unittest

from xmc.auth import pkce


class PkceTest(unittest.TestCase):
    """Tests PKCE verifier, challenge, and state generation."""

    def test_s256_challenge_matches_rfc7636_appendix_b(self) -> None:
        """The S256 transform matches the RFC 7636 Appendix B test vector."""
        verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
        self.assertEqual(
            pkce.s256_challenge(verifier), "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
        )

    def test_new_verifier_is_rfc7636_compliant(self) -> None:
        """Verifiers use the unreserved alphabet within 43-128 chars and are random."""
        verifier = pkce.new_verifier()
        self.assertRegex(verifier, r"^[A-Za-z0-9._~-]{43,128}$")
        self.assertEqual(len(pkce.s256_challenge(verifier)), 43)
        self.assertNotEqual(verifier, pkce.new_verifier())

    def test_new_state_matches_server_contract(self) -> None:
        """State values satisfy the server's 1-128 char [A-Za-z0-9_-] contract."""
        state = pkce.new_state()
        self.assertRegex(state, r"^[A-Za-z0-9_-]{43,128}$")
        self.assertNotEqual(state, pkce.new_state())


if __name__ == "__main__":
    unittest.main()
