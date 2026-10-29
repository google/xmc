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
import sys
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

    def test_cleanup_stale_bundled_packages_removes_directory(self) -> None:
        """Test cleanup_stale_bundled_packages purges wheel-provided xdash directory."""
        with tempfile.TemporaryDirectory() as tmp_str:
            site_pkgs = Path(tmp_str) / "site-packages"
            stale_xdash = site_pkgs / "xdash"
            stale_xdash.mkdir(parents=True)
            (stale_xdash / "__init__.py").write_text("", encoding="utf-8")
            (stale_xdash / "builders.py").write_text("", encoding="utf-8")
            (stale_xdash / "auth.py").write_text("", encoding="utf-8")

            install_xmanager.cleanup_stale_bundled_packages(site_pkgs)
            self.assertFalse(stale_xdash.exists())

    def test_cleanup_stale_bundled_packages_noop_when_missing(self) -> None:
        """Test cleanup_stale_bundled_packages completes cleanly when xdash does not exist."""
        with tempfile.TemporaryDirectory() as tmp_str:
            site_pkgs = Path(tmp_str) / "site-packages"
            site_pkgs.mkdir(parents=True)

            install_xmanager.cleanup_stale_bundled_packages(site_pkgs)
            self.assertFalse((site_pkgs / "xdash").exists())

    def test_cleanup_stale_bundled_packages_preserves_symlinks(self) -> None:
        """Test cleanup_stale_bundled_packages does not delete symlinked directories."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            site_pkgs = root / "site-packages"
            site_pkgs.mkdir(parents=True)
            external_dir = root / "external_xdash"
            external_dir.mkdir(parents=True)
            (external_dir / "__init__.py").write_text("", encoding="utf-8")

            symlink_xdash = site_pkgs / "xdash"
            symlink_xdash.symlink_to(external_dir, target_is_directory=True)

            install_xmanager.cleanup_stale_bundled_packages(site_pkgs)
            self.assertTrue(symlink_xdash.is_symlink())
            self.assertTrue(external_dir.exists())

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
                "install_xmanager.cleanup_stale_bundled_packages",
            ) as mock_cleanup,
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

        mock_cleanup.assert_called_once_with(site_pkgs)
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
            mock.patch(
                "install_xmanager.cleanup_stale_bundled_packages",
            ) as mock_cleanup,
            mock.patch("install_xmanager._ensure_longrunning_operations_proto"),
            mock.patch("install_xmanager.stage_local_protos", return_value=["api.proto"]),
            mock.patch(
                "subprocess.run",
                side_effect=subprocess.CalledProcessError(1, ["protoc"]),
            ),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            install_xmanager.compile_local_protos(venv_python, Path("/mock/repo"))

        mock_cleanup.assert_called_once_with(site_pkgs)

    def test_install_local_sdks_invokes_pip_for_each_present_sdk(self) -> None:
        """Test install_local_sdks installs every present LOCAL_SDKS dir with --no-deps."""
        venv_python = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_str:
            repo_root = Path(tmp_str)
            for rel_dir in install_xmanager.LOCAL_SDKS.values():
                (repo_root / rel_dir).mkdir(parents=True)

            with mock.patch("subprocess.run") as mock_run:
                install_xmanager.install_local_sdks(venv_python, repo_root)

            self.assertEqual(
                mock_run.call_args_list,
                [
                    mock.call(
                        [
                            "/mock/venv/bin/python3",
                            "-m",
                            "pip",
                            "install",
                            "--no-deps",
                            "-e",
                            str(repo_root / rel_dir),
                        ],
                        check=True,
                    )
                    for rel_dir in install_xmanager.LOCAL_SDKS.values()
                ],
            )

    def test_install_local_sdks_skips_missing_sdk(self) -> None:
        """Test install_local_sdks only installs SDK directories that exist."""
        venv_python = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_str:
            repo_root = Path(tmp_str)
            xdash_dir = repo_root / install_xmanager.LOCAL_SDKS["xdash"]
            xdash_dir.mkdir(parents=True)

            with mock.patch("subprocess.run") as mock_run:
                install_xmanager.install_local_sdks(venv_python, repo_root)

            mock_run.assert_called_once_with(
                [
                    "/mock/venv/bin/python3",
                    "-m",
                    "pip",
                    "install",
                    "--no-deps",
                    "-e",
                    str(xdash_dir),
                ],
                check=True,
            )

    def test_install_local_sdks_noop_when_missing(self) -> None:
        """Test install_local_sdks cleanly no-ops when no SDK directory is present."""
        venv_python = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_str:
            repo_root = Path(tmp_str)

            with mock.patch("subprocess.run") as mock_run:
                install_xmanager.install_local_sdks(venv_python, repo_root)

            mock_run.assert_not_called()

    def test_resolve_module_origin(self) -> None:
        """Test resolve_module_origin parses only the marked origin line in isolated mode."""
        venv_python = Path("/mock/venv/bin/python3")
        marker = install_xmanager._MODULE_ORIGIN_MARKER
        origin = "/repo/sdks/python/xdash/__init__.py"
        cases = (
            ("clean", f"{marker}{origin}\n", Path(origin)),
            ("startup_noise_before", f"Warning: deprecated\n{marker}{origin}\n", Path(origin)),
            ("noise_after", f"{marker}{origin}\nbye\n", Path(origin)),
            ("not_found", f"{marker}\n", None),
            ("no_marker", "Warning: deprecated\n", None),
        )
        for name, stdout, expected in cases:
            with self.subTest(name):
                completed = subprocess.CompletedProcess(
                    args=[], returncode=0, stdout=stdout, stderr=""
                )
                with mock.patch("subprocess.run", return_value=completed) as mock_run:
                    resolved = install_xmanager.resolve_module_origin(venv_python, "xdash")

                self.assertEqual(resolved, expected)
                mock_run.assert_called_once_with(
                    [
                        "/mock/venv/bin/python3",
                        "-I",
                        "-c",
                        install_xmanager._MODULE_ORIGIN_SNIPPET,
                        "xdash",
                    ],
                    check=True,
                    capture_output=True,
                    text=True,
                )

    def test_check_sdk_provenance(self) -> None:
        """Test check_sdk_provenance accepts repo origins and rejects shadowed or missing ones."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            expected_dir = root / "repo" / "sdks" / "python" / "xdash"
            shadow = root / "venv" / "site-packages" / "xdash" / "__init__.py"
            cases = (
                ("repo_origin", expected_dir / "__init__.py", None),
                ("nested_repo_origin", expected_dir / "sub" / "mod.py", None),
                ("shadowed_by_site_packages", shadow, f"resolved to {shadow}"),
                ("sibling_prefix_dir", root / "repo/sdks/python/xdash_old/__init__.py", "resolved"),
                ("not_importable", None, "is not importable"),
            )
            for name, origin, expected_error in cases:
                with self.subTest(name):
                    if expected_error is None:
                        install_xmanager.check_sdk_provenance("xdash", origin, expected_dir)
                        continue
                    with self.assertRaisesRegex(install_xmanager.SdkShadowedError, expected_error):
                        install_xmanager.check_sdk_provenance("xdash", origin, expected_dir)

    def test_verify_sdk_provenance_checks_present_sdks(self) -> None:
        """Test verify_sdk_provenance resolves and checks only SDK dirs present in the repo."""
        venv_python = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_str:
            repo_root = Path(tmp_str)
            xdash_dir = repo_root / install_xmanager.LOCAL_SDKS["xdash"]
            xdash_dir.mkdir(parents=True)
            origin = xdash_dir / "__init__.py"

            with (
                mock.patch(
                    "install_xmanager.resolve_module_origin", return_value=origin
                ) as mock_resolve,
                mock.patch("install_xmanager.check_sdk_provenance") as mock_check,
            ):
                install_xmanager.verify_sdk_provenance(venv_python, repo_root)

            mock_resolve.assert_called_once_with(venv_python, "xdash")
            mock_check.assert_called_once_with("xdash", origin, xdash_dir)

    def test_verify_sdk_provenance_propagates_shadowing(self) -> None:
        """Test verify_sdk_provenance raises SdkShadowedError for a wheel-shadowed SDK."""
        venv_python = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_str:
            repo_root = Path(tmp_str)
            (repo_root / install_xmanager.LOCAL_SDKS["xdash"]).mkdir(parents=True)
            shadow = Path("/mock/venv/site-packages/xdash/__init__.py")

            with (
                mock.patch("install_xmanager.resolve_module_origin", return_value=shadow),
                self.assertRaises(install_xmanager.SdkShadowedError),
            ):
                install_xmanager.verify_sdk_provenance(venv_python, repo_root)

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

    @mock.patch("install_xmanager.verify_sdk_provenance")
    @mock.patch("install_xmanager.verify_imports")
    @mock.patch("install_xmanager.install_local_sdks")
    @mock.patch("install_xmanager.compile_local_protos")
    @mock.patch("install_xmanager.install_xmanager_package")
    @mock.patch("install_xmanager.setup_virtualenv")
    def test_main_execution(
        self,
        mock_venv: mock.Mock,
        mock_install: mock.Mock,
        mock_compile: mock.Mock,
        mock_install_sdks: mock.Mock,
        mock_verify: mock.Mock,
        mock_provenance: mock.Mock,
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
        mock_install_sdks.assert_called_once_with(
            Path("/mock/venv/bin/python3"),
            install_xmanager.REPO_ROOT,
        )
        mock_verify.assert_called_once_with(Path("/mock/venv/bin/python3"))
        mock_provenance.assert_called_once_with(
            Path("/mock/venv/bin/python3"),
            install_xmanager.REPO_ROOT,
        )

    def test_main_checks_provenance_before_imports(self) -> None:
        """Test main runs the provenance check before verify_imports for actionable errors."""
        calls = mock.Mock()
        with (
            mock.patch(
                "install_xmanager.setup_virtualenv",
                return_value=Path("/mock/venv/bin/python3"),
            ),
            mock.patch("install_xmanager.install_xmanager_package"),
            mock.patch("install_xmanager.compile_local_protos"),
            mock.patch("install_xmanager.install_local_sdks"),
            mock.patch("install_xmanager.verify_sdk_provenance", calls.provenance),
            mock.patch("install_xmanager.verify_imports", calls.imports),
            tempfile.TemporaryDirectory() as tmp_venv,
        ):
            install_xmanager.main(["--venv", tmp_venv])

        self.assertEqual([c[0] for c in calls.mock_calls], ["provenance", "imports"])

    def test_resolve_module_origin_with_real_interpreter(self) -> None:
        """Test the origin probe snippet end-to-end with the current stdlib interpreter."""
        python = Path(sys.executable)
        json_origin = install_xmanager.resolve_module_origin(python, "json")
        self.assertIsNotNone(json_origin)
        assert json_origin is not None
        self.assertEqual(json_origin.parent.name, "json")
        self.assertIsNone(install_xmanager.resolve_module_origin(python, "no_such_module_xyz"))

    @mock.patch(
        "install_xmanager.verify_sdk_provenance",
        side_effect=install_xmanager.SdkShadowedError("xdash resolved to /stale"),
    )
    @mock.patch("install_xmanager.verify_imports")
    @mock.patch("install_xmanager.install_local_sdks")
    @mock.patch("install_xmanager.compile_local_protos")
    @mock.patch("install_xmanager.install_xmanager_package")
    @mock.patch(
        "install_xmanager.setup_virtualenv",
        return_value=Path("/mock/venv/bin/python3"),
    )
    def test_main_exits_when_sdk_shadowed(self, *_mocks: mock.Mock) -> None:
        """Test main exits non-zero with the shadowing details when provenance fails."""
        with (
            tempfile.TemporaryDirectory() as tmp_venv,
            mock.patch("sys.stderr", new_callable=io.StringIO) as stderr,
            self.assertRaises(SystemExit) as ctx,
        ):
            install_xmanager.main(["--venv", tmp_venv])

        self.assertEqual(ctx.exception.code, 1)
        self.assertIn("xdash resolved to /stale", stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
