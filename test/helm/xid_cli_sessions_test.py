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

"""Checks that the XID service reads the same ESS database as ESS, for CLI session checks."""

import re
import unittest

from chart_test_lib import render_template, require_helm


def _rendered_value(template: str, key: str, values: dict[str, str]) -> str:
    match = re.search(rf'{key}: "([^"]*)"', render_template(template, values))
    if match is None:
        raise AssertionError(f"{key} not rendered in {template}")
    return match.group(1)


class XidCliSessionsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_ess_database_matches_ess(self) -> None:
        cases = {
            "default": {},
            "override": {
                "experimentStateServer.spannerDatabaseName": "projects/p/instances/i/databases/ess"
            },
        }
        for case, values in cases.items():
            with self.subTest(case=case):
                self.assertEqual(
                    _rendered_value(
                        "templates/xid/configmap.yaml", "xid_ess_spanner_database_name", values
                    ),
                    _rendered_value(
                        "templates/ess/configmap.yaml", "ess_spanner_database_name", values
                    ),
                )


if __name__ == "__main__":
    unittest.main()
