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

"""Checks that a comma-separated allowed_emails list reaches the ESS unchanged.

The blueprint hands auth.allowedEmails to Helm as a `set` value, and Helm splits `set` values on
unescaped commas. The documented "user1@example.com,user2@example.com" format therefore failed
the xmc_app deployment with `key "com" has no value`. The blueprint now escapes each comma, and
Helm strips the escapes again, so the chart must still render the plain list.
"""

import re
import unittest

from chart_test_lib import (
    BLUEPRINT_PATH,
    extract_module_block,
    find_repo_file,
    parse_set_values,
    render_template,
    require_helm,
)

ESS_DEPLOYMENT = "templates/ess/deployment.yaml"
_ESCAPING_EXPRESSION = r"""$(replace(vars.allowed_emails, ",", "\\,"))"""


def escape_like_blueprint(emails: str) -> str:
    """Returns what the blueprint's Terraform replace() expression hands to Helm."""
    return emails.replace(",", "\\,")


def rendered_allowed_emails(helm_value: str) -> str:
    """Renders the ESS deployment and returns its ALLOWED_EMAILS environment value."""
    manifest = render_template(
        ESS_DEPLOYMENT, {"essDeployment.enabled": "true", "auth.allowedEmails": helm_value}
    )
    match = re.search(r'- name: ALLOWED_EMAILS\n\s+value: "(.*)"', manifest)
    if match is None:
        raise AssertionError(f"ALLOWED_EMAILS not found in {ESS_DEPLOYMENT}")
    return match.group(1)


class BlueprintEscapingTest(unittest.TestCase):
    """Covers the blueprint side, which needs no helm binary."""

    def test_blueprint_escapes_commas_in_allowed_emails(self) -> None:
        with open(find_repo_file(BLUEPRINT_PATH), encoding="utf-8") as blueprint:
            block = extract_module_block(blueprint.read(), "xmc_app_helm")
        set_values = parse_set_values(block)
        self.assertEqual(_ESCAPING_EXPRESSION, set_values.get("auth.allowedEmails"))


class AllowedEmailsRenderingTest(unittest.TestCase):
    """Covers how Helm parses the escaped value and what the chart renders from it."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_escaped_list_renders_as_plain_comma_separated_list(self) -> None:
        for emails in (
            "user1@example.com,user2@example.com",
            "user1@example.com,user2@example.com,ci@example.iam.gserviceaccount.com",
            "solo@example.com",
        ):
            with self.subTest(emails=emails):
                self.assertEqual(emails, rendered_allowed_emails(escape_like_blueprint(emails)))

    def test_unescaped_list_is_rejected_by_helm(self) -> None:
        """Pins the Helm behaviour that makes the blueprint escaping necessary."""
        with self.assertRaisesRegex(RuntimeError, 'key "com" has no value'):
            rendered_allowed_emails("user1@example.com,user2@example.com")


if __name__ == "__main__":
    unittest.main()
