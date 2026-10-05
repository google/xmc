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

"""Unit tests for blueprint_compiler."""

import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env import blueprint_compiler

_DEPLOYMENT = "xmc-v2-test"


class PurgeStagedArtifactsTest(unittest.TestCase):
    """Tests for purge_staged_artifacts."""

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp_dir.name)
        self.output_dir = self.root / "ctk-out"
        self.ghpc_dir = self.output_dir / _DEPLOYMENT / ".ghpc"
        self.staged_dir = self.ghpc_dir / "staged"

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    def _write(self, path: Path, content: str = "x") -> Path:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        return path

    def test_removes_stale_staged_file(self) -> None:
        stale = self._write(self.staged_dir / "helm_abc" / "templates" / "rbac.yaml")

        blueprint_compiler.purge_staged_artifacts(self.output_dir, _DEPLOYMENT)

        self.assertFalse(stale.exists())
        self.assertFalse(self.staged_dir.exists())

    def test_leaves_siblings_untouched(self) -> None:
        self._write(self.staged_dir / "values.yaml_123")
        siblings = [
            self._write(self.ghpc_dir / "artifacts" / "expanded_blueprint.yaml"),
            self._write(self.output_dir / _DEPLOYMENT / "xmc_app" / "terraform.tfstate"),
            self._write(self.output_dir / "other-deployment" / ".ghpc" / "staged" / "f"),
        ]

        blueprint_compiler.purge_staged_artifacts(self.output_dir, _DEPLOYMENT)

        for sibling in siblings:
            self.assertTrue(sibling.is_file(), sibling)

    def test_missing_staged_dir_is_noop(self) -> None:
        blueprint_compiler.purge_staged_artifacts(self.output_dir, _DEPLOYMENT)

        self.assertFalse(self.output_dir.exists())

    def test_output_dir_symlink_is_followed(self) -> None:
        stale = self._write(self.staged_dir / "helm_abc" / "Chart.yaml")
        link = self.root / "ctk-out-link"
        link.symlink_to(self.output_dir)

        blueprint_compiler.purge_staged_artifacts(link, _DEPLOYMENT)

        self.assertFalse(stale.exists())

    def test_rejects_unsafe_deployment_names(self) -> None:
        for name in ("", ".", "..", "../other", "a/b", "/abs"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                blueprint_compiler.purge_staged_artifacts(self.output_dir, name)

    def test_rejects_symlinked_components(self) -> None:
        outside = self.root / "outside"
        victim = self._write(outside / "precious.txt")
        cases = {
            "deployment": self.output_dir / _DEPLOYMENT,
            "ghpc": self.ghpc_dir,
            "staged": self.staged_dir,
        }
        for label, link in cases.items():
            with self.subTest(component=label):
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(outside, target_is_directory=True)
                with self.assertRaises(ValueError):
                    blueprint_compiler.purge_staged_artifacts(self.output_dir, _DEPLOYMENT)
                self.assertTrue(victim.is_file())
                link.unlink()


class CompileBlueprintTest(unittest.TestCase):
    """Tests for compile_blueprint."""

    @mock.patch.object(blueprint_compiler, "run_command")
    @mock.patch.object(blueprint_compiler, "compute_chart_content_hash", return_value="abc123")
    @mock.patch.object(blueprint_compiler, "purge_staged_artifacts")
    def test_purges_before_gcluster_create(self, mock_purge, mock_hash, mock_run) -> None:
        order = mock.Mock()
        order.attach_mock(mock_purge, "purge")
        order.attach_mock(mock_run, "run")

        blueprint_compiler.compile_blueprint("my-config.yaml", _DEPLOYMENT)

        mock_hash.assert_called_once_with(Path("deploy/helm"))
        self.assertEqual(
            order.mock_calls,
            [
                mock.call.purge(Path("ctk-out"), _DEPLOYMENT),
                mock.call.run(
                    [
                        "./cluster-toolkit/gcluster",
                        "create",
                        "deploy/ctk_v2/xmanager-cloud.yaml",
                        "-d",
                        "my-config.yaml",
                        "-o",
                        "ctk-out",
                        "--vars",
                        "helm_chart_content_hash=abc123",
                        "-l",
                        "WARNING",
                        "-w",
                        "--force",
                    ]
                ),
            ],
        )

    @mock.patch.object(blueprint_compiler, "run_command")
    @mock.patch.object(blueprint_compiler, "compute_chart_content_hash", return_value="abc123")
    @mock.patch.object(blueprint_compiler, "purge_staged_artifacts")
    def test_passes_enable_components_var(self, _mock_purge, _mock_hash, mock_run) -> None:
        blueprint_compiler.compile_blueprint("my-config.yaml", _DEPLOYMENT, enable_components=True)

        mock_run.assert_called_once_with(
            [
                "./cluster-toolkit/gcluster",
                "create",
                "deploy/ctk_v2/xmanager-cloud.yaml",
                "-d",
                "my-config.yaml",
                "-o",
                "ctk-out",
                "--vars",
                "helm_chart_content_hash=abc123",
                "--vars",
                "enable_components=true",
                "-l",
                "WARNING",
                "-w",
                "--force",
            ]
        )


if __name__ == "__main__":
    unittest.main()
