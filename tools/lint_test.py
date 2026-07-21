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

import os
import tempfile
import unittest

from tools.lint import format_yaml_file, lint_file, should_ignore


class LintTest(unittest.TestCase):
    def test_should_ignore(self):
        self.assertTrue(should_ignore("deploy/helm/templates/deployment.yaml"))
        self.assertTrue(should_ignore("deploy/helm/templates_disabled/service.yaml"))
        self.assertTrue(should_ignore("node_modules/foo/bar.yaml"))
        self.assertFalse(should_ignore("deploy/ctk_v2/xmanager-cloud.yaml"))
        self.assertFalse(should_ignore(".github/workflows/unit-tests.yml"))

    def test_format_yaml_file_trailing_whitespace(self):
        with tempfile.NamedTemporaryFile("w+", delete=False, suffix=".yaml") as f:
            f.write("key: value   \n\n\n\n")
            filepath = f.name

        try:
            modified = format_yaml_file(filepath)
            self.assertTrue(modified)
            with open(filepath, "r") as f:
                content = f.read()
            self.assertEqual(content, "key: value\n")
        finally:
            os.remove(filepath)

    def test_lint_file_valid(self):
        with tempfile.NamedTemporaryFile("w+", delete=False, suffix=".yaml") as f:
            f.write("key: value\nsub:\n  nested: value2\n")
            filepath = f.name

        try:
            issues = lint_file(filepath)
            self.assertEqual(issues, [])
        finally:
            os.remove(filepath)

    def test_lint_file_tab_indent(self):
        with tempfile.NamedTemporaryFile("w+", delete=False, suffix=".yaml") as f:
            f.write("key: value\n\tsub: value2\n")
            filepath = f.name

        try:
            issues = lint_file(filepath)
            self.assertTrue(any("tabs instead of spaces" in issue for issue in issues))
        finally:
            os.remove(filepath)


if __name__ == "__main__":
    unittest.main()
