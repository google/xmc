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

"""Unit tests for YAML boolean parsing and flattening."""

import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.yaml_parser import (
        _scan_yaml_lines,
        parse_yaml_booleans,
    )
except ImportError:
    from yaml_parser import (  # type: ignore[no-redef]
        _scan_yaml_lines,
        parse_yaml_booleans,
    )


class YamlParserTest(unittest.TestCase):
    """Tests for YAML boolean flag parser."""

    def test_empty_content(self) -> None:
        self.assertEqual(parse_yaml_booleans(""), {})
        self.assertEqual(parse_yaml_booleans("   \n\n  "), {})

    def test_flat_and_nested_booleans(self) -> None:
        yaml_content = """
        vars:
          enable_components: false
          rod_deployment_enabled: true
          use_redis_queue: true
          blueprint_name: "xmc-core"
        componentsDeployment:
          enabled: false
        rodDeployment:
          enabled: true
        healthService:
          deployment:
            enabled: false
        """
        flags = parse_yaml_booleans(yaml_content)
        self.assertEqual(flags.get("enable_components"), False)
        self.assertEqual(flags.get("rod_deployment_enabled"), True)
        self.assertEqual(flags.get("use_redis_queue"), True)
        self.assertEqual(flags.get("componentsDeployment.enabled"), False)
        self.assertEqual(flags.get("rodDeployment.enabled"), True)
        self.assertEqual(flags.get("healthService.deployment.enabled"), False)
        self.assertNotIn("blueprint_name", flags)

    def test_scan_yaml_lines_fallback(self) -> None:
        yaml_content = """
        # Comment line
        vars:
          enable_components: false
          rod_deployment_enabled: true
        componentsDeployment:
          enabled: true
        healthService:
          deployment:
            enabled: false
        """
        flags = _scan_yaml_lines(yaml_content)
        self.assertEqual(flags.get("enable_components"), False)
        self.assertEqual(flags.get("rod_deployment_enabled"), True)
        self.assertEqual(flags.get("componentsDeployment.enabled"), True)
        self.assertEqual(flags.get("healthService.deployment.enabled"), False)

    def test_parse_yaml_booleans_non_dict_fallback(self) -> None:
        yaml_content = """
        - not a dict
        - just a list
        vars:
          enable_components: true
        """
        flags = parse_yaml_booleans(yaml_content)
        self.assertEqual(flags.get("enable_components"), True)


if __name__ == "__main__":
    unittest.main()
