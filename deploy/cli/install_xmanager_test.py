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
import subprocess
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
        git_req = "xmanager @ git+https://github.com/google-deepmind/xmanager@main"
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec(git_req),
            git_req,
        )

    def test_resolve_xmanager_spec_comparison_operators(self) -> None:
        """Test resolve_xmanager_spec prefixes leading comparison operators."""
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec("==0.8.2"),
            "xmanager==0.8.2",
        )
        self.assertEqual(
            install_xmanager.resolve_xmanager_spec(">=0.8.2,<0.9"),
            "xmanager>=0.8.2,<0.9",
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
            self.assertEqual(args.requirements, [])

    def test_parse_args_env_overrides(self) -> None:
        """Test parse_args respects VENV_DIR and XMANAGER_VERSION env vars."""
        env = {"VENV_DIR": "/env/venv", "XMANAGER_VERSION": "0.8.2"}
        with mock.patch.dict("os.environ", env, clear=True):
            args = install_xmanager.parse_args([])
            self.assertEqual(args.venv, "/env/venv")
            self.assertEqual(args.xmanager_version, "0.8.2")

    def test_parse_args_custom_cli(self) -> None:
        """Test parse_args with explicit CLI arguments including repeatable --requirements."""
        cli_args = [
            "--venv",
            "/custom/venv",
            "--xmanager-version",
            "0.8.4",
            "--requirements",
            "req1.txt",
            "-r",
            "req2.txt",
        ]
        args = install_xmanager.parse_args(cli_args)
        self.assertEqual(args.venv, "/custom/venv")
        self.assertEqual(args.xmanager_version, "0.8.4")
        self.assertEqual(args.requirements, ["req1.txt", "req2.txt"])

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
        """Test install_xmanager_package upgrades pip, installs requirements, then xmanager."""
        venv_python = Path("/mock/venv/bin/python3")
        with mock.patch("subprocess.run") as mock_run:
            install_xmanager.install_xmanager_package(
                venv_python,
                "xmanager==0.8.2",
                ["/path/to/requirements.txt"],
            )

        self.assertEqual(
            mock_run.call_args_list,
            [
                mock.call(
                    ["/mock/venv/bin/python3", "-m", "pip", "install", "--upgrade", "pip"],
                    check=True,
                ),
                mock.call(
                    [
                        "/mock/venv/bin/python3",
                        "-m",
                        "pip",
                        "install",
                        "-r",
                        "/path/to/requirements.txt",
                    ],
                    check=True,
                ),
                mock.call(
                    [
                        "/mock/venv/bin/python3",
                        "-m",
                        "pip",
                        "install",
                        "xmanager==0.8.2",
                        *install_xmanager.RUNTIME_DEPENDENCIES,
                    ],
                    check=True,
                ),
            ],
        )

    def test_install_xmanager_package_failure_propagates(self) -> None:
        """Test install_xmanager_package propagates CalledProcessError on pip failure."""
        venv_python = Path("/mock/venv/bin/python3")
        with (
            mock.patch(
                "subprocess.run",
                side_effect=subprocess.CalledProcessError(1, ["pip"]),
            ),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            install_xmanager.install_xmanager_package(venv_python, "xmanager==0.8.2")

    def test_resolve_venv_proto_paths(self) -> None:
        """Test resolve_venv_proto_paths returns site-packages and grpc_tools _proto paths."""
        site_pkgs = Path("/mock/site-packages")
        grpc_proto = site_pkgs / "grpc_tools" / "_proto"
        mock_res = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=f"{site_pkgs}\n{grpc_proto}\n",
            stderr="",
        )
        with mock.patch("subprocess.run", return_value=mock_res):
            resolved_site, resolved_grpc = install_xmanager.resolve_venv_proto_paths(
                Path("/mock/venv/bin/python3")
            )

        self.assertEqual(resolved_site, site_pkgs)
        self.assertEqual(resolved_grpc, grpc_proto)

    def test_ensure_longrunning_operations_proto(self) -> None:
        """Test _ensure_longrunning_operations_proto copies operations_proto.proto when needed."""
        with tempfile.TemporaryDirectory() as tmp_str:
            site_pkgs = Path(tmp_str) / "site-packages"
            longrunning_dir = site_pkgs / "google" / "longrunning"
            longrunning_dir.mkdir(parents=True)
            (longrunning_dir / "operations_proto.proto").write_text(
                "syntax = 'proto3';",
                encoding="utf-8",
            )

            install_xmanager._ensure_longrunning_operations_proto(site_pkgs)
            self.assertTrue((longrunning_dir / "operations.proto").exists())

    def test_stage_local_protos_success_and_removes_stale(self) -> None:
        """Test stage_local_protos removes stale .proto files, copies fresh ones, and touches __init__.py."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            repo_root = root / "repo"
            site_pkgs = root / "site-packages"

            stale_dir = site_pkgs / "xmanager_cloud" / "xid_service" / "proto"
            stale_dir.mkdir(parents=True)
            stale_proto = stale_dir / "obsolete.proto"
            stale_proto.write_text("syntax = 'proto3';", encoding="utf-8")

            for src_subdir, _ in install_xmanager.PROTO_PACKAGE_DIRS:
                proto_dir = repo_root / "api" / "protos" / src_subdir
                proto_dir.mkdir(parents=True)
                (proto_dir / "api.proto").write_text("syntax = 'proto3';", encoding="utf-8")

            staged = install_xmanager.stage_local_protos(repo_root, site_pkgs)
            self.assertEqual(len(staged), 3)
            self.assertFalse(stale_proto.exists())
            for _, dst_pkg in install_xmanager.PROTO_PACKAGE_DIRS:
                dst_dir = site_pkgs / "xmanager_cloud" / dst_pkg / "proto"
                self.assertTrue((dst_dir / "__init__.py").exists())
                self.assertTrue((dst_dir / "api.proto").exists())

    def test_stage_local_protos_missing_dir(self) -> None:
        """Test stage_local_protos exits when api/protos directory is missing."""
        with (
            tempfile.TemporaryDirectory() as tmp_str,
            mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
            self.assertRaises(SystemExit),
        ):
            root = Path(tmp_str)
            install_xmanager.stage_local_protos(root / "empty_repo", root / "site-packages")
        self.assertIn("Local proto directory not found", stderr.getvalue())

    def test_compile_local_protos(self) -> None:
        """Test compile_local_protos ensures operations.proto and invokes grpc_tools.protoc."""
        venv_python = Path("/mock/venv/bin/python3")
        site_pkgs = Path("/mock/site-packages")
        grpc_proto = Path("/mock/grpc_proto")
        staged_files = ["/mock/site-packages/xmanager_cloud/xid_service/proto/api.proto"]

        with (
            mock.patch(
                "install_xmanager.resolve_venv_proto_paths",
                return_value=(site_pkgs, grpc_proto),
            ),
            mock.patch(
                "install_xmanager._ensure_longrunning_operations_proto",
            ) as mock_ensure_ops,
            mock.patch(
                "install_xmanager.stage_local_protos",
                return_value=staged_files,
            ),
            mock.patch("subprocess.run") as mock_run,
        ):
            install_xmanager.compile_local_protos(venv_python, Path("/mock/repo"))

        mock_ensure_ops.assert_called_once_with(site_pkgs)
        mock_run.assert_called_once_with(
            [
                "/mock/venv/bin/python3",
                "-m",
                "grpc_tools.protoc",
                "-I/mock/site-packages",
                "-I/mock/grpc_proto",
                "--python_out=/mock/site-packages",
                "--grpc_python_out=/mock/site-packages",
                *staged_files,
            ],
            check=True,
        )

    def test_compile_local_protos_protoc_failure_propagates(self) -> None:
        """Test compile_local_protos propagates CalledProcessError when protoc fails."""
        venv_python = Path("/mock/venv/bin/python3")
        site_pkgs = Path("/mock/site-packages")
        grpc_proto = Path("/mock/grpc_proto")
        with (
            mock.patch(
                "install_xmanager.resolve_venv_proto_paths",
                return_value=(site_pkgs, grpc_proto),
            ),
            mock.patch("install_xmanager._ensure_longrunning_operations_proto"),
            mock.patch("install_xmanager.stage_local_protos", return_value=["api.proto"]),
            mock.patch(
                "subprocess.run",
                side_effect=subprocess.CalledProcessError(1, ["protoc"]),
            ),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            install_xmanager.compile_local_protos(venv_python, Path("/mock/repo"))

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
    @mock.patch("install_xmanager.compile_local_protos")
    @mock.patch("install_xmanager.install_xmanager_package")
    @mock.patch("install_xmanager.setup_virtualenv")
    def test_main_execution(
        self,
        mock_venv: mock.Mock,
        mock_install: mock.Mock,
        mock_compile: mock.Mock,
        mock_verify: mock.Mock,
    ) -> None:
        """Test main execution flow with mocked external operations."""
        mock_venv.return_value = Path("/mock/venv/bin/python3")

        with tempfile.TemporaryDirectory() as tmp_venv:
            install_xmanager.main(
                [
                    "--venv",
                    tmp_venv,
                    "--xmanager-version",
                    "0.8.2",
                    "--requirements",
                    "requirements.txt",
                ]
            )

        mock_venv.assert_called_once_with(Path(tmp_venv).resolve())
        mock_install.assert_called_once_with(
            Path("/mock/venv/bin/python3"),
            "xmanager==0.8.2",
            ["requirements.txt"],
        )
        mock_compile.assert_called_once_with(
            Path("/mock/venv/bin/python3"),
            install_xmanager.REPO_ROOT,
        )
        mock_verify.assert_called_once_with(Path("/mock/venv/bin/python3"))


if __name__ == "__main__":
    unittest.main()
