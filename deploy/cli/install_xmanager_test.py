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

"""Unit tests for install_xmanager script."""

import io
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import install_xmanager


class TestInstallXmanager(unittest.TestCase):
    """Test cases for install_xmanager functions."""

    def test_resolve_xmanager_spec_default(self) -> None:
        """Test resolve_xmanager_spec returns default specifier when empty."""
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec(""),
            install_xmanager.DEFAULT_XMANAGER_SPEC,
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("   "),
            install_xmanager.DEFAULT_XMANAGER_SPEC,
        )

    def test_resolve_xmanager_spec_version_tag(self) -> None:
        """Test resolve_xmanager_spec formats version tag as exact pin."""
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("0.8.4"),
            "xmanager==0.8.4",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec(" 0.8.2 "),
            "xmanager==0.8.2",
        )

    def test_resolve_xmanager_spec_full_requirement(self) -> None:
        """Test resolve_xmanager_spec preserves full xmanager requirement specifier."""
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("xmanager>=0.8.2"),
            "xmanager>=0.8.2",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("xmanager==0.8.4"),
            "xmanager==0.8.4",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("XManager>=0.8.2"),
            "XManager>=0.8.2",
        )

    def test_resolve_xmanager_spec_comparison_operators(self) -> None:
        """Test resolve_xmanager_spec prefixes leading comparison operators."""
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("==0.8.2"),
            "xmanager==0.8.2",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec(">=0.8.2"),
            "xmanager>=0.8.2",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("~=0.8.2"),
            "xmanager~=0.8.2",
        )

    def test_parse_args_defaults(self) -> None:
        """Test parse_args default options when environment is empty."""
        with mock.patch.dict("os.environ", {}, clear=True):
            args = install_xmanager.parse_args([])
            self.assertEqual(args.venv, "./venv")
            self.assertEqual(args.xmanager_version, "")

    def test_parse_args_env_overrides(self) -> None:
        """Test parse_args respects VENV_DIR and XMANAGER_VERSION env vars."""
        env = {"VENV_DIR": "/env/venv", "XMANAGER_VERSION": "0.8.2"}
        with mock.patch.dict("os.environ", env, clear=True):
            args = install_xmanager.parse_args([])
            self.assertEqual(args.venv, "/env/venv")
            self.assertEqual(args.xmanager_version, "0.8.2")

    def test_parse_args_custom_cli(self) -> None:
        """Test parse_args with explicit CLI arguments."""
        cli_args = [
            "--venv",
            "/custom/venv",
            "--xmanager-version",
            "0.8.4",
        ]
        args = install_xmanager.parse_args(cli_args)
        self.assertEqual(args.venv, "/custom/venv")
        self.assertEqual(args.xmanager_version, "0.8.4")

    def test_check_prerequisites_success(self) -> None:
        """Test check_prerequisites when python3 binary is found."""
        with mock.patch("shutil.which", return_value="/usr/bin/python3"):
            install_xmanager.check_prerequisites()

    def test_check_prerequisites_missing(self) -> None:
        """Test check_prerequisites exits when python3 is missing."""
        with (
            mock.patch("shutil.which", return_value=None),
            mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
            self.assertRaises(SystemExit),
        ):
            install_xmanager.check_prerequisites()
        self.assertIn("python3 is required", stderr.getvalue())

    @mock.patch("venv.create")
    def test_setup_virtualenv_creates_when_missing_python(
        self,
        mock_create: mock.Mock,
    ) -> None:
        """Test setup_virtualenv runs venv.create when python binary is absent."""
        with tempfile.TemporaryDirectory() as tmp_str:
            venv_dir = Path(tmp_str)

            def _create_venv(target_dir: Path, with_pip: bool = False) -> None:
                del with_pip
                python_bin = target_dir / "bin" / "python3"
                python_bin.parent.mkdir(parents=True, exist_ok=True)
                python_bin.write_text("", encoding="utf-8")

            mock_create.side_effect = _create_venv
            result = install_xmanager.setup_virtualenv(venv_dir)
            mock_create.assert_called_once_with(venv_dir, with_pip=True)
            self.assertEqual(result, venv_dir / "bin" / "python3")

    @mock.patch("venv.create")
    def test_setup_virtualenv_reuses_existing_python3(
        self,
        mock_create: mock.Mock,
    ) -> None:
        """Test setup_virtualenv skips venv.create when bin/python3 exists."""
        with tempfile.TemporaryDirectory() as tmp_str:
            venv_dir = Path(tmp_str)
            python_bin = venv_dir / "bin" / "python3"
            python_bin.parent.mkdir(parents=True)
            python_bin.write_text("", encoding="utf-8")
            result = install_xmanager.setup_virtualenv(venv_dir)
            mock_create.assert_not_called()
            self.assertEqual(result, python_bin)

    @mock.patch("venv.create")
    def test_setup_virtualenv_falls_back_to_python_binary(
        self,
        mock_create: mock.Mock,
    ) -> None:
        """Test setup_virtualenv falls back to bin/python when bin/python3 is absent."""
        with tempfile.TemporaryDirectory() as tmp_str:
            venv_dir = Path(tmp_str)
            python_bin = venv_dir / "bin" / "python"
            python_bin.parent.mkdir(parents=True)
            python_bin.write_text("", encoding="utf-8")
            result = install_xmanager.setup_virtualenv(venv_dir)
            mock_create.assert_not_called()
            self.assertEqual(result, python_bin)

    def test_install_xmanager_package(self) -> None:
        """Test install_xmanager_package upgrades pip and installs packages."""
        venv_python = Path("/mock/venv/bin/python3")
        with mock.patch("subprocess.run") as mock_run:
            install_xmanager.install_xmanager_package(venv_python, "xmanager==0.8.2")

        self.assertEqual(mock_run.call_count, 2)
        mock_run.assert_any_call(
            ["/mock/venv/bin/python3", "-m", "pip", "install", "--upgrade", "pip"],
            check=True,
        )
        mock_run.assert_any_call(
            [
                "/mock/venv/bin/python3",
                "-m",
                "pip",
                "install",
                "xmanager==0.8.2",
                *install_xmanager.RUNTIME_DEPENDENCIES,
            ],
            check=True,
        )

    def test_verify_imports(self) -> None:
        """Test verify_imports checks all required XManager and proto modules."""
        venv_python = Path("/mock/venv/bin/python3")
        with mock.patch("subprocess.run") as mock_run:
            install_xmanager.verify_imports(venv_python)

        self.assertEqual(mock_run.call_count, len(install_xmanager.VERIFIED_MODULES))
        for mod in install_xmanager.VERIFIED_MODULES:
            mock_run.assert_any_call(
                ["/mock/venv/bin/python3", "-c", f"import {mod}"],
                check=True,
            )

    @mock.patch("install_xmanager.verify_imports")
    @mock.patch("install_xmanager.install_xmanager_package")
    @mock.patch("install_xmanager.setup_virtualenv")
    @mock.patch("install_xmanager.check_prerequisites")
    def test_main_execution(
        self,
        mock_pre: mock.Mock,
        mock_venv: mock.Mock,
        mock_install: mock.Mock,
        mock_verify: mock.Mock,
    ) -> None:
        """Test main execution flow with mocked external operations."""
        mock_venv.return_value = Path("/mock/venv/bin/python3")

        with tempfile.TemporaryDirectory() as tmp_venv:
            install_xmanager.main(["--venv", tmp_venv, "--xmanager-version", "0.8.2"])

        mock_pre.assert_called_once()
        mock_venv.assert_called_once_with(Path(tmp_venv).resolve())
        mock_install.assert_called_once_with(
            Path("/mock/venv/bin/python3"),
            "xmanager==0.8.2",
        )
        mock_verify.assert_called_once_with(Path("/mock/venv/bin/python3"))


if __name__ == "__main__":
    unittest.main()
