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

"""Checks that session lifetime overrides (browser and CLI) reach the authentication backend."""

import unittest

from chart_test_lib import render_template, require_helm


class SessionTTLTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_session_ttl_reaches_ess(self) -> None:
        for ttl in (None, "168h", "720h", "0s", "invalid"):
            with self.subTest(ttl=ttl):
                values = {"essDeployment.enabled": "true"}
                if ttl is not None:
                    values["auth.sessionTtl"] = ttl
                manifest = render_template("templates/ess/deployment.yaml", values)
                expected = "1h" if ttl is None else ttl
                self.assertRegex(
                    manifest,
                    rf'- name: SESSION_TTL\n\s+value: "{expected}"',
                )

    def test_cli_session_ttl_reaches_ess(self) -> None:
        for ttl in (None, "1h", "720h"):
            with self.subTest(ttl=ttl):
                values = {"essDeployment.enabled": "true"}
                if ttl is not None:
                    values["auth.cliSessionTtl"] = ttl
                manifest = render_template("templates/ess/deployment.yaml", values)
                expected = "12h" if ttl is None else ttl
                self.assertRegex(manifest, rf'- name: CLI_SESSION_TTL\n\s+value: "{expected}"')


if __name__ == "__main__":
    unittest.main()
