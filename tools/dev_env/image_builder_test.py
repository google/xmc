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
import unittest
from unittest import mock

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.image_builder import ImageBuilder


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

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_execute(self, mock_run: mock.MagicMock) -> None:
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

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_empty_specs(self, mock_run: mock.MagicMock) -> None:
        images = self.builder.build_images(services=[])
        self.assertEqual(images, [])
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_build_images_bytes_stdout(self, mock_run: mock.MagicMock) -> None:
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
        self.assertEqual(mock_run.call_count, 2)
        mock_run.assert_has_calls(
            [
                mock.call(
                    [
                        "kind",
                        "load",
                        "docker-image",
                        "img1:latest",
                        "--name",
                        "test-kind",
                    ]
                ),
                mock.call(
                    [
                        "kind",
                        "load",
                        "docker-image",
                        "img2:latest",
                        "--name",
                        "test-kind",
                    ]
                ),
            ]
        )

    @mock.patch("tools.dev_env.image_builder.run_command")
    def test_load_images_into_kind_empty(self, mock_run: mock.MagicMock) -> None:
        self.builder.load_images_into_kind([])
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
