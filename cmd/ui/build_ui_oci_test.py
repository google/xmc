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

"""Unit tests for cmd/ui/build_ui_oci.py."""

import hashlib
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, call, patch

_DIR = os.path.dirname(os.path.abspath(__file__))
if _DIR not in sys.path:
    sys.path.insert(0, _DIR)

import build_ui_oci  # noqa: E402


class BuildUiOciTest(unittest.TestCase):
    """Test suite for build_ui_oci module."""

    def setUp(self):
        super().setUp()
        self.test_dir = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)
        super().tearDown()

    def test_get_sha256_computes_correct_hash(self):
        """Verifies get_sha256 calculates SHA256 over multi-chunk file contents."""
        test_file = os.path.join(self.test_dir, "sample.txt")
        # 100KB payload to ensure multi-chunk reading loop (chunk size 65536)
        content = b"a" * 102400
        with open(test_file, "wb") as f:
            f.write(content)

        expected_hash = hashlib.sha256(content).hexdigest()
        actual_hash = build_ui_oci.get_sha256(test_file)
        self.assertEqual(actual_hash, expected_hash)

    def test_copy_flat_copies_sources(self):
        """Verifies copy_flat copies sources and static assets."""
        src_dir = os.path.join(self.test_dir, "src")
        dest_dir = os.path.join(self.test_dir, "dest")
        os.makedirs(os.path.join(src_dir, "app"), exist_ok=True)

        ts_path = os.path.join(src_dir, "app", "main.ts")
        with open(ts_path, "w", encoding="utf-8") as f:
            f.write("import '@angular/material';")

        static_path = os.path.join(src_dir, "app", "logo.svg")
        with open(static_path, "w", encoding="utf-8") as f:
            f.write("<svg></svg>")

        build_ui_oci.copy_flat(src_dir, dest_dir)

        dest_ts = os.path.join(dest_dir, "app", "main.ts")
        self.assertTrue(os.path.isfile(dest_ts))
        with open(dest_ts, encoding="utf-8") as f:
            self.assertEqual(f.read(), "import '@angular/material';")

        dest_static = os.path.join(dest_dir, "app", "logo.svg")
        self.assertTrue(os.path.isfile(dest_static))
        with open(dest_static, encoding="utf-8") as f:
            self.assertEqual(f.read(), "<svg></svg>")

    def test_copy_flat_ignores_transient_and_build_files(self):
        """Verifies copy_flat skips build artifacts, python files, and ignored directories."""
        src_dir = os.path.join(self.test_dir, "src")
        dest_dir = os.path.join(self.test_dir, "dest")
        os.makedirs(os.path.join(src_dir, "node_modules", "pkg"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, "dist", "browser"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, ".angular"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, "build_tmp"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, ".git"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, "__pycache__"), exist_ok=True)
        os.makedirs(os.path.join(src_dir, "app"), exist_ok=True)

        with open(os.path.join(src_dir, "node_modules", "pkg", "index.js"), "w") as f:
            f.write("module.exports = {};")
        with open(os.path.join(src_dir, "dist", "browser", "main.js"), "w") as f:
            f.write("console.log('bundle');")
        with open(os.path.join(src_dir, "BUILD.bazel"), "w") as f:
            f.write("py_library(name = 'lib')")
        with open(os.path.join(src_dir, "build_ui_oci.py"), "w") as f:
            f.write("print('builder')")
        with open(os.path.join(src_dir, "app", "app.component.ts"), "w") as f:
            f.write("export class App {}")

        build_ui_oci.copy_flat(src_dir, dest_dir)

        self.assertTrue(os.path.isfile(os.path.join(dest_dir, "app", "app.component.ts")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "node_modules")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "dist")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, ".angular")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "build_tmp")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, ".git")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "__pycache__")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "BUILD.bazel")))
        self.assertFalse(os.path.exists(os.path.join(dest_dir, "build_ui_oci.py")))

    def test_copy_flat_guards_against_symlink_cycles(self):
        """Verifies copy_flat terminates safely when encountering circular directory symlinks."""
        src_dir = os.path.join(self.test_dir, "src")
        dest_dir = os.path.join(self.test_dir, "dest")
        sub_dir = os.path.join(src_dir, "sub")
        os.makedirs(sub_dir, exist_ok=True)

        with open(os.path.join(sub_dir, "valid.ts"), "w") as f:
            f.write("export const x = 1;")

        # Create a circular symlink pointing back to src_dir
        os.symlink(src_dir, os.path.join(sub_dir, "cycle_link"))

        build_ui_oci.copy_flat(src_dir, dest_dir)

        self.assertTrue(os.path.isfile(os.path.join(dest_dir, "sub", "valid.ts")))

    def test_copy_flat_prunes_symlinks_pointing_to_ignored_dirs(self):
        """Verifies copy_flat does not traverse symlinks pointing into node_modules or dist."""
        src_dir = os.path.join(self.test_dir, "src")
        dest_dir = os.path.join(self.test_dir, "dest")
        node_modules = os.path.join(src_dir, "node_modules")
        os.makedirs(node_modules, exist_ok=True)
        with open(os.path.join(node_modules, "pkg.js"), "w") as f:
            f.write("ignored")

        os.makedirs(os.path.join(src_dir, "app"), exist_ok=True)
        # Symlink named 'external_deps' pointing directly into node_modules
        os.symlink(node_modules, os.path.join(src_dir, "app", "external_deps"))

        build_ui_oci.copy_flat(src_dir, dest_dir)

        self.assertFalse(os.path.exists(os.path.join(dest_dir, "app", "external_deps")))

    def test_copy_flat_re_raises_oserror(self):
        """Verifies copy_flat re-raises OSError when file operations fail."""
        src_dir = os.path.join(self.test_dir, "src")
        dest_dir = os.path.join(self.test_dir, "dest")
        os.makedirs(src_dir, exist_ok=True)
        with open(os.path.join(src_dir, "test.ts"), "w") as f:
            f.write("export const a = 1;")

        with patch("shutil.copy2", side_effect=OSError("Disk write error")):
            with patch("builtins.open", side_effect=OSError("Read error")):
                with self.assertRaises(OSError):
                    build_ui_oci.copy_flat(src_dir, dest_dir)

    @patch("build_ui_oci.subprocess.run")
    @patch("build_ui_oci.platform.machine", return_value="x86_64")
    def test_main_fast_path_when_prebuilt_dist_exists(self, mock_machine, mock_run):
        """Verifies main selects fast single-stage Nginx build when pre-compiled bundle exists."""
        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        dist_browser = os.path.join(ui_dir, "dist", "browser")
        os.makedirs(dist_browser, exist_ok=True)

        with open(os.path.join(dist_browser, "index.html"), "w") as f:
            f.write("<html><body>UI</body></html>")
        with open(os.path.join(ui_dir, "nginx.conf"), "w") as f:
            f.write("server {}")
        with open(os.path.join(ui_dir, "generate_config.sh"), "w") as f:
            f.write("#!/bin/sh")

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                build_ui_oci.main()

        # Verify docker build and save were invoked
        mock_run.assert_has_calls(
            [
                call(["docker", "build", "-t", "ui-image:latest", unittest.mock.ANY], check=True),
                call(["docker", "save", "-o", output_tar, "ui-image:latest"], check=True),
            ]
        )

    @patch("build_ui_oci.copy_flat")
    @patch("build_ui_oci.subprocess.run")
    @patch("build_ui_oci.platform.machine", return_value="x86_64")
    def test_main_fallback_path_when_dist_absent(self, mock_machine, mock_run, mock_copy_flat):
        """Verifies main selects multi-stage build and calls copy_flat when dist is missing."""
        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        os.makedirs(ui_dir, exist_ok=True)

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                build_ui_oci.main()

        mock_copy_flat.assert_called_once()
        mock_run.assert_has_calls(
            [
                call(["docker", "build", "-t", "ui-image:latest", unittest.mock.ANY], check=True),
                call(["docker", "save", "-o", output_tar, "ui-image:latest"], check=True),
            ]
        )

    @patch("build_ui_oci.subprocess.run")
    @patch("build_ui_oci.platform.machine", return_value="arm64")
    def test_main_arm64_defaults_to_linux_amd64(self, mock_machine, mock_run):
        """Verifies arm64 host defaults target platform to linux/amd64."""
        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        dist_browser = os.path.join(ui_dir, "dist", "browser")
        os.makedirs(dist_browser, exist_ok=True)
        with open(os.path.join(dist_browser, "index.html"), "w") as f:
            f.write("<html></html>")
        with open(os.path.join(ui_dir, "nginx.conf"), "w") as f:
            f.write("server {}")
        with open(os.path.join(ui_dir, "generate_config.sh"), "w") as f:
            f.write("#!/bin/sh")

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.dict(os.environ, {}, clear=True):
                with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                    build_ui_oci.main()

        build_call = mock_run.call_args_list[0]
        cmd = build_call[0][0]
        self.assertIn("--platform", cmd)
        self.assertIn("linux/amd64", cmd)

    @patch("build_ui_oci.subprocess.run")
    @patch("build_ui_oci.platform.machine", return_value="x86_64")
    def test_main_explicit_platform_override(self, mock_machine, mock_run):
        """Verifies XMC_UI_PLATFORM environment variable overrides target platform."""
        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        dist_browser = os.path.join(ui_dir, "dist", "browser")
        os.makedirs(dist_browser, exist_ok=True)
        with open(os.path.join(dist_browser, "index.html"), "w") as f:
            f.write("<html></html>")
        with open(os.path.join(ui_dir, "nginx.conf"), "w") as f:
            f.write("server {}")
        with open(os.path.join(ui_dir, "generate_config.sh"), "w") as f:
            f.write("#!/bin/sh")

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.dict(os.environ, {"XMC_UI_PLATFORM": "linux/arm64"}, clear=True):
                with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                    build_ui_oci.main()

        build_call = mock_run.call_args_list[0]
        cmd = build_call[0][0]
        self.assertIn("--platform", cmd)
        self.assertIn("linux/arm64", cmd)

    def test_main_exits_on_missing_args(self):
        """Verifies main prints usage and exits with code 1 if output_tar is not specified."""
        with patch.object(sys, "argv", ["build_ui_oci.py"]):
            with self.assertRaises(SystemExit) as cm:
                build_ui_oci.main()
            self.assertEqual(cm.exception.code, 1)

    @patch(
        "build_ui_oci.subprocess.run", side_effect=subprocess.CalledProcessError(1, "docker build")
    )
    @patch("build_ui_oci.platform.machine", return_value="x86_64")
    def test_main_handles_docker_build_failure(self, mock_machine, mock_run):
        """Verifies main exits with code 1 when docker build command fails."""
        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        dist_browser = os.path.join(ui_dir, "dist", "browser")
        os.makedirs(dist_browser, exist_ok=True)
        with open(os.path.join(dist_browser, "index.html"), "w") as f:
            f.write("<html></html>")
        with open(os.path.join(ui_dir, "nginx.conf"), "w") as f:
            f.write("server {}")
        with open(os.path.join(ui_dir, "generate_config.sh"), "w") as f:
            f.write("#!/bin/sh")

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                with self.assertRaises(SystemExit) as cm:
                    build_ui_oci.main()
                self.assertEqual(cm.exception.code, 1)

    @patch("build_ui_oci.subprocess.run")
    @patch("build_ui_oci.platform.machine", return_value="x86_64")
    def test_main_handles_docker_save_failure(self, mock_machine, mock_run):
        """Verifies main exits with code 1 when docker save command fails."""
        # Make build succeed but save fail
        mock_run.side_effect = [
            MagicMock(),
            subprocess.CalledProcessError(1, "docker save"),
        ]

        fake_workspace = os.path.join(self.test_dir, "workspace")
        ui_dir = os.path.join(fake_workspace, "cmd", "ui")
        dist_browser = os.path.join(ui_dir, "dist", "browser")
        os.makedirs(dist_browser, exist_ok=True)
        with open(os.path.join(dist_browser, "index.html"), "w") as f:
            f.write("<html></html>")
        with open(os.path.join(ui_dir, "nginx.conf"), "w") as f:
            f.write("server {}")
        with open(os.path.join(ui_dir, "generate_config.sh"), "w") as f:
            f.write("#!/bin/sh")

        output_tar = os.path.join(self.test_dir, "out.tar")

        with patch("os.getcwd", return_value=fake_workspace):
            with patch.object(sys, "argv", ["build_ui_oci.py", output_tar]):
                with self.assertRaises(SystemExit) as cm:
                    build_ui_oci.main()
                self.assertEqual(cm.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
