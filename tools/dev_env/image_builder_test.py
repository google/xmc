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

"""Unit tests for tools.dev_env.image_builder."""

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.image_builder import ImageBuilder, discover_buildable_services


class ImageBuilderTest(unittest.TestCase):
    """Test suite for ImageBuilder container building and KinD loading."""

    def setUp(self) -> None:
        super().setUp()
        self.config = LocalDevConfig(
            project_id="test-proj",
            cluster_name="test-kind",
            enabled_services=["experiment_state_server", "ui", "custom_svc"],
        )
        self.builder = ImageBuilder(config=self.config)

    def test_discover_buildable_services(self) -> None:
        services = discover_buildable_services()
        self.assertIn("actuator", services)
        self.assertIn("experiment_state_server", services)
        self.assertIn("ui", services)
        self.assertIn("reconciler", services)

    def test_discover_buildable_services_temp_dir(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            tmproot = Path(tmpdir)
            cmd_dir = tmproot / "cmd"
            cmd_dir.mkdir()
            (cmd_dir / "svc_a").mkdir()
            (cmd_dir / "svc_a" / "BUILD.bazel").write_text(
                'oci_image_for_service(name="svc_a_image", binary=":svc_a")'
            )
            (cmd_dir / "svc_b").mkdir()
            (cmd_dir / "svc_b" / "BUILD").write_text('genrule(name="svc_b_image_tarball")')
            (cmd_dir / "non_image_svc").mkdir()
            (cmd_dir / "non_image_svc" / "BUILD.bazel").write_text('go_binary(name="cli")')

            discovered = discover_buildable_services(tmproot)
            self.assertEqual(discovered, ["svc_a", "svc_b"])

    def test_discover_buildable_services_no_cmd_dir(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            self.assertEqual(discover_buildable_services(Path(tmpdir)), [])

    def test_get_service_specs_all(self) -> None:
        specs = self.builder.get_service_specs(services=["all"])
        service_names = [pkg.replace("cmd/", "") for pkg, _, _ in specs]
        self.assertIn("actuator", service_names)
        self.assertIn("ui", service_names)

    def test_get_service_specs(self) -> None:
        specs = self.builder.get_service_specs()
        self.assertEqual(len(specs), 3)
        self.assertEqual(
            specs[0],
            (
                "cmd/experiment_state_server",
                "experiment_state_server_image_tarball",
                "experiment_state_server_image:latest",
            ),
        )
        self.assertEqual(specs[1], ("cmd/ui", "ui_image_tarball", "ui_image:latest"))
        self.assertEqual(
            specs[2],
            ("cmd/custom_svc", "custom_svc_image_tarball", "custom_svc_image:latest"),
        )

    def test_get_service_specs_deduplicates(self) -> None:
        specs = self.builder.get_service_specs(services=["ui", "ui", "actuator"])
        self.assertEqual(len(specs), 2)
        self.assertEqual(specs[0], ("cmd/ui", "ui_image_tarball", "ui_image:latest"))
        self.assertEqual(
            specs[1], ("cmd/actuator", "actuator_image_tarball", "actuator_image:latest")
        )

    def test_get_service_specs_invalid_name(self) -> None:
        with self.assertRaises(ValueError):
            self.builder.get_service_specs(services=["../invalid_path"])

    def test_build_images_skip_build(self) -> None:
        images = self.builder.build_images(skip_build=True)
        self.assertEqual(
            images,
            [
                "experiment_state_server_image:latest",
                "ui_image:latest",
                "custom_svc_image:latest",
            ],
        )

    def test_get_service_specs_custom_mixed_case_lowercased(self) -> None:
        specs = self.builder.get_service_specs(services=["MixedCaseSvc"])
        self.assertEqual(len(specs), 1)
        self.assertEqual(
            specs[0],
            ("cmd/MixedCaseSvc", "MixedCaseSvc_image_tarball", "mixedcasesvc_image:latest"),
        )

    def test_resolve_tarball_path_default_exists(self) -> None:
        with mock.patch.object(Path, "is_file", return_value=True):
            tar = self.builder._resolve_tarball_path(Path("/fake/bazel-bin"), "cmd/ui", "ui_tar")
            self.assertEqual(tar, Path("/fake/bazel-bin/cmd/ui/ui_tar.tar"))

    def test_resolve_tarball_path_bazel_out_fallback(self) -> None:
        def fake_is_file(path_self: Path) -> bool:
            return "/bazel-out/" in str(path_self)

        def fake_is_dir(path_self: Path) -> bool:
            return "bazel-out" in str(path_self)

        fallback_path = Path("/repo/bazel-out/k8-opt/bin/cmd/ui/ui_tar.tar")
        with (
            mock.patch.object(Path, "is_file", fake_is_file),
            mock.patch.object(Path, "is_dir", fake_is_dir),
            mock.patch.object(Path, "glob", return_value=[fallback_path]),
        ):
            tar = self.builder._resolve_tarball_path(Path("/fake/bazel-bin"), "cmd/ui", "ui_tar")
            self.assertEqual(tar, fallback_path)

    def test_resolve_tarball_path_not_found_raises(self) -> None:
        with (
            mock.patch.object(Path, "is_file", return_value=False),
            mock.patch.object(Path, "is_dir", return_value=False),
        ):
            with self.assertRaises(FileNotFoundError):
                self.builder._resolve_tarball_path(Path("/fake/bazel-bin"), "cmd/ui", "ui_tar")

    @mock.patch.object(ImageBuilder, "_resolve_tarball_path", return_value=Path("/fake/tar.tar"))
    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_execute(
        self, mock_run: mock.MagicMock, mock_resolve: mock.MagicMock
    ) -> None:
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=["bazel", "build"], returncode=0),
            subprocess.CompletedProcess(
                args=["bazel", "info"],
                returncode=0,
                stdout="/path/to/bazel-bin\n",
            ),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
        ]
        images = self.builder.build_images(skip_build=False)
        self.assertEqual(len(images), 3)
        self.assertTrue(mock_run.called)
        self.assertEqual(mock_resolve.call_count, 3)

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_empty_specs(self, mock_run: mock.MagicMock) -> None:
        images = self.builder.build_images(services=[])
        self.assertEqual(images, [])
        mock_run.assert_not_called()

    @mock.patch.object(ImageBuilder, "_resolve_tarball_path", return_value=Path("/fake/tar.tar"))
    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_bytes_stdout(
        self, mock_run: mock.MagicMock, mock_resolve: mock.MagicMock
    ) -> None:
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=["bazel", "build"], returncode=0),
            subprocess.CompletedProcess(
                args=["bazel", "info"],
                returncode=0,
                stdout=b"/path/to/bazel-bin\n",
            ),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
            subprocess.CompletedProcess(args=["docker", "load"], returncode=0),
        ]
        images = self.builder.build_images(skip_build=False)
        self.assertEqual(len(images), 3)
        self.assertEqual(mock_resolve.call_count, 3)

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_empty_bazel_bin(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=["bazel", "build"], returncode=0),
            subprocess.CompletedProcess(
                args=["bazel", "info"],
                returncode=0,
                stdout="",
            ),
        ]
        with self.assertRaises(RuntimeError):
            self.builder.build_images(skip_build=False)

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_load_images_into_kind(self, mock_run: mock.MagicMock) -> None:
        self.builder.load_images_into_kind(["img1:latest", "img2:latest"])
        mock_run.assert_called_once_with(
            [
                "kind",
                "load",
                "docker-image",
                "img1:latest",
                "img2:latest",
                "--name",
                "test-kind",
            ]
        )

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_load_images_into_kind_empty(self, mock_run: mock.MagicMock) -> None:
        self.builder.load_images_into_kind([])
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
