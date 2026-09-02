#!/usr/bin/env python3
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

"""Unit tests for tools.dev_env.blueprint_selection."""

import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env import blueprint_selection

DEPLOYMENT = "xmc-dev"
OTHER_BLUEPRINT = "deploy/ctk_v2/other-blueprint.yaml"


class BlueprintSelectionTest(unittest.TestCase):
    """Tests recording and resolving the blueprint a deployment is compiled from."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.output_root = Path(self.tmp_dir.name) / "ctk-out"
        patcher = mock.patch.object(blueprint_selection, "DEPLOYMENT_OUTPUT_ROOT", self.output_root)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.addCleanup(self.tmp_dir.cleanup)

    def make_deployment_dir(self) -> Path:
        """Creates the compiled-deployment directory record_blueprint writes into."""
        deployment_dir = self.output_root / DEPLOYMENT
        deployment_dir.mkdir(parents=True)
        return deployment_dir

    def test_resolve_defaults_to_greenfield_blueprint(self):
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT),
            blueprint_selection.DEFAULT_BLUEPRINT_PATH,
        )

    def test_resolve_prefers_requested_blueprint(self):
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT, OTHER_BLUEPRINT), OTHER_BLUEPRINT
        )

    def test_resolve_ignores_blank_request(self):
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT, "   "),
            blueprint_selection.DEFAULT_BLUEPRINT_PATH,
        )

    def test_resolve_strips_surrounding_whitespace(self):
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT, f"  {OTHER_BLUEPRINT}\n"),
            OTHER_BLUEPRINT,
        )

    def test_resolve_reuses_recorded_blueprint(self):
        self.make_deployment_dir()
        blueprint_selection.record_blueprint(DEPLOYMENT, OTHER_BLUEPRINT)
        self.assertEqual(blueprint_selection.resolve_blueprint(DEPLOYMENT), OTHER_BLUEPRINT)

    def test_request_overrides_recorded_blueprint(self):
        self.make_deployment_dir()
        blueprint_selection.record_blueprint(DEPLOYMENT, OTHER_BLUEPRINT)
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT, "deploy/ctk_v2/custom.yaml"),
            "deploy/ctk_v2/custom.yaml",
        )

    def test_record_is_scoped_to_one_deployment(self):
        self.make_deployment_dir()
        blueprint_selection.record_blueprint(DEPLOYMENT, OTHER_BLUEPRINT)
        self.assertEqual(
            blueprint_selection.resolve_blueprint("other-deployment"),
            blueprint_selection.DEFAULT_BLUEPRINT_PATH,
        )

    def test_recording_twice_keeps_the_latest_choice(self):
        self.make_deployment_dir()
        blueprint_selection.record_blueprint(DEPLOYMENT, OTHER_BLUEPRINT)
        blueprint_selection.record_blueprint(DEPLOYMENT, "deploy/ctk_v2/xmanager-cloud.yaml")
        self.assertEqual(
            blueprint_selection.recorded_blueprint(DEPLOYMENT), "deploy/ctk_v2/xmanager-cloud.yaml"
        )

    def test_record_skipped_when_deployment_never_compiled(self):
        blueprint_selection.record_blueprint(DEPLOYMENT, OTHER_BLUEPRINT)
        self.assertFalse(self.output_root.exists())
        self.assertIsNone(blueprint_selection.recorded_blueprint(DEPLOYMENT))

    def test_recorded_blueprint_is_none_without_a_record(self):
        self.make_deployment_dir()
        self.assertIsNone(blueprint_selection.recorded_blueprint(DEPLOYMENT))

    def test_empty_record_is_treated_as_absent(self):
        deployment_dir = self.make_deployment_dir()
        (deployment_dir / ".xmc-blueprint").write_text("\n", encoding="utf-8")
        self.assertIsNone(blueprint_selection.recorded_blueprint(DEPLOYMENT))
        self.assertEqual(
            blueprint_selection.resolve_blueprint(DEPLOYMENT),
            blueprint_selection.DEFAULT_BLUEPRINT_PATH,
        )


if __name__ == "__main__":
    unittest.main()
