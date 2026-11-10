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

"""Hermetic unit tests for blueprint module extraction and set_values parsing."""

import unittest

from chart_test_lib import (
    BLUEPRINT_PATH,
    extract_module_block,
    find_repo_file,
    parse_set_values,
)


class BlueprintParsingTest(unittest.TestCase):
    """Covers blueprint module extraction and set_values parsing without requiring helm."""

    def test_extract_module_block_and_parse_set_values_edge_cases(self) -> None:
        """Covers comments, quotes, spaces, # in quoted strings, multibyte values, and missing modules."""
        sample = (
            "- group: prereqs\n"
            "  modules:\n"
            "  - id: mod_a # comment\n"
            "    settings:\n"
            "      set_values:\n"
            "      - name: 'key.one'\n"
            "        # inline comment with multibyte — ✓\n"
            '        value: "val one" # trailing comment\n'
            "      - name: 'key.two'\n"
            "        value: 'val #two' # trailing comment\n"
            "  - id: mod_b\n"
            "    settings:\n"
            "      set_values:\n"
            "      - name: other.key\n"
            "        value: other-val\n"
            "- group: next_group\n"
            "  modules:\n"
            "  - id: mod_c\n"
        )
        block_a = extract_module_block(sample, "mod_a")
        block_b = extract_module_block(sample, "mod_b")
        self.assertEqual({"key.one": "val one", "key.two": "val #two"}, parse_set_values(block_a))
        self.assertEqual({"other.key": "other-val"}, parse_set_values(block_b))
        self.assertEqual({}, parse_set_values(""))
        with self.assertRaises(AssertionError):
            extract_module_block(sample, "missing_mod")

    def test_blueprint_xmc_app_helm_module_is_parseable(self) -> None:
        """Verifies extract_module_block and parse_set_values against the real blueprint."""
        with open(find_repo_file(BLUEPRINT_PATH), encoding="utf-8") as blueprint_file:
            block = extract_module_block(blueprint_file.read(), "xmc_app_helm")
        set_values = parse_set_values(block)
        self.assertEqual("true", set_values.get("essDeployment.enabled"))


if __name__ == "__main__":
    unittest.main()
