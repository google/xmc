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

"""Checks that ui.docsUrl reaches the UI deployment as XMC_DOCS_URL."""

import unittest

from chart_test_lib import render_template, require_helm


class UiDocsUrlTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_docs_url_reaches_ui_deployment(self) -> None:
        for docs_url in (None, "https://docs.example.com/xmc"):
            with self.subTest(docs_url=docs_url):
                values = {"ui.enabled": "true"}
                if docs_url is not None:
                    values["ui.docsUrl"] = docs_url
                manifest = render_template("templates/ess/ui-deployment.yaml", values)
                expected = "https://github.com/google/xmc" if docs_url is None else docs_url
                self.assertRegex(
                    manifest,
                    rf'- name: XMC_DOCS_URL\n\s+value: "{expected}"',
                )


if __name__ == "__main__":
    unittest.main()
