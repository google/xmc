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

"""Unit tests for feature flags discovery, token normalization, and transitions."""

import sys
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.feature_flags import (
        derive_display_name,
        derive_flag_tokens,
        discover_feature_flags,
        extract_flag_transitions,
        get_current_feature_states,
        normalize_flag_name,
    )
except ImportError:
    from feature_flags import (  # type: ignore[no-redef]
        derive_display_name,
        derive_flag_tokens,
        discover_feature_flags,
        extract_flag_transitions,
        get_current_feature_states,
        normalize_flag_name,
    )


class FeatureFlagsTest(unittest.TestCase):
    """Tests for feature flag discovery and token management."""

    def test_normalize_flag_name(self) -> None:
        self.assertEqual(normalize_flag_name("enable_components"), "components")
        self.assertEqual(normalize_flag_name("use_redis_queue"), "redis_queue")
        self.assertEqual(normalize_flag_name("rod_deployment_enabled"), "rod")
        self.assertEqual(normalize_flag_name("componentsDeployment.enabled"), "components")
        self.assertEqual(
            normalize_flag_name("vars.health_service_deployment_enabled"),
            "health_service",
        )
        self.assertEqual(normalize_flag_name("feature.enabled"), "feature")

    def test_derive_flag_tokens(self) -> None:
        rod_tokens = derive_flag_tokens("rod_deployment_enabled")
        self.assertIn("rod", rod_tokens)
        self.assertIn("rod_data_provider", rod_tokens)

        comp_tokens = derive_flag_tokens("enable_components")
        self.assertIn("components", comp_tokens)
        self.assertIn("component", comp_tokens)
        self.assertIn("cms", comp_tokens)

        health_tokens = derive_flag_tokens("health_service_deployment_enabled")
        self.assertIn("health", health_tokens)
        self.assertIn("health_service", health_tokens)
        self.assertIn("admin_panel", health_tokens)

        dash_tokens = derive_flag_tokens("dashboard_service_deployment_enabled")
        self.assertIn("dashboard", dash_tokens)
        self.assertIn("xdash", dash_tokens)

        metrics_tokens = derive_flag_tokens("enable_metrics")
        self.assertIn("metrics", metrics_tokens)
        self.assertIn("ingestion_gateway", metrics_tokens)

    def test_derive_display_name(self) -> None:
        self.assertEqual(
            derive_display_name("rod_deployment_enabled"),
            "Resource Observability Dashboard (ROD)",
        )
        self.assertEqual(
            derive_display_name("cms_enabled"),
            "Component Management Service (CMS)",
        )
        self.assertEqual(
            derive_display_name("dashboard_service_deployment_enabled"),
            "Dashboard Service & xdash SDK",
        )
        self.assertEqual(
            derive_display_name("health_service_deployment_enabled"),
            "Health Service & Admin Panel",
        )
        self.assertEqual(derive_display_name("enable_components"), "Components Engine")
        self.assertEqual(
            derive_display_name("enable_metrics"),
            "Metrics & Ingestion Gateway",
        )
        self.assertEqual(derive_display_name("enable_batch_evaluator"), "Batch Evaluator")

    @patch("tools.release.feature_flags.get_git_file_content")
    def test_discover_feature_flags(self, mock_get_content: MagicMock) -> None:
        ctk_content = """
        vars:
          enable_components: false
          rod_deployment_enabled: true
        """
        helm_content = """
        componentsDeployment:
          enabled: false
        rodDeployment:
          enabled: true
        """

        def mock_content(ref: str, path: str, _repo: Path) -> str:
            if "ctk_v2" in path:
                return ctk_content
            return helm_content

        mock_get_content.side_effect = mock_content

        flags = discover_feature_flags("1.0.0", Path("."))
        self.assertIn("components", flags)
        self.assertIn("rod", flags)
        self.assertFalse(flags["components"].is_active)
        self.assertTrue(flags["rod"].is_active)

    @patch("tools.release.feature_flags.get_git_file_content")
    def test_extract_flag_transitions_all_cases(self, mock_get_content: MagicMock) -> None:
        base_yaml = """
        vars:
          enable_components: false
          enable_metrics: true
          deprecated_flag: true
        """
        target_yaml = """
        vars:
          enable_components: true
          enable_metrics: false
          newly_added_flag: false
        """

        def mock_content(ref: str, path: str, _repo: Path) -> str:
            if "ctk_v2" in path:
                return base_yaml if ref == "0.5.0" else target_yaml
            return ""

        mock_get_content.side_effect = mock_content

        transitions = extract_flag_transitions("0.5.0", "0.6.0", Path("."))
        self.assertEqual(len(transitions), 4)

        comp_t = next(t for t in transitions if t.flag_name == "enable_components")
        self.assertTrue(comp_t.is_promotion_to_default)

        metrics_t = next(t for t in transitions if t.flag_name == "enable_metrics")
        self.assertTrue(metrics_t.is_flip)
        self.assertFalse(metrics_t.is_promotion_to_default)

        new_t = next(t for t in transitions if t.flag_name == "newly_added_flag")
        self.assertIsNone(new_t.base_value)
        self.assertFalse(new_t.target_value)

        dep_t = next(t for t in transitions if t.flag_name == "deprecated_flag")
        self.assertTrue(dep_t.base_value)
        self.assertIsNone(dep_t.target_value)

    @patch("tools.release.feature_flags.get_git_file_content")
    def test_get_current_feature_states(self, mock_get_content: MagicMock) -> None:
        ctk_content = "vars:\n  enable_components: false\n  rod_deployment_enabled: true\n"
        mock_get_content.side_effect = lambda _ref, path, _repo: (
            ctk_content if "ctk_v2" in path else ""
        )
        states = get_current_feature_states("0.6.0", Path("."))
        self.assertFalse(states["components"])
        self.assertTrue(states["rod"])


if __name__ == "__main__":
    unittest.main()
