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

    def test_parse_github_url_valid_tree(self) -> None:
        """Test parse_github_url with standard tree URL."""
        url = "https://github.com/google/xmc/tree/main/api/protos"
        repo, branch, subdir = install_xmanager.parse_github_url(url)
        self.assertEqual(repo, "https://github.com/google/xmc.git")
        self.assertEqual(branch, "main")
        self.assertEqual(subdir, "api/protos")

    def test_parse_github_url_valid_root(self) -> None:
        """Test parse_github_url with repo root URL."""
        url = "https://github.com/google/xmc"
        repo, branch, subdir = install_xmanager.parse_github_url(url)
        self.assertEqual(repo, "https://github.com/google/xmc.git")
        self.assertEqual(branch, "main")
        self.assertEqual(subdir, "api/protos")

    def test_parse_github_url_invalid(self) -> None:
        """Test parse_github_url with invalid URL format."""
        with self.assertRaises(SystemExit):
            install_xmanager.parse_github_url("invalid-url")

    def test_parse_args_defaults(self) -> None:
        """Test parse_args default options."""
        dummy_path = Path("/dummy/deploy/cli/install_xmanager.py")
        with mock.patch.dict("os.environ", {}, clear=True):
            args = install_xmanager.parse_args([], script_path=dummy_path)
            self.assertEqual(
                args.protos_loc,
                "https://github.com/google/xmc/tree/main/api/protos",
            )
            self.assertEqual(args.xmanager_branch, "xm-cloud")
            self.assertEqual(args.venv, "./venv")
            self.assertFalse(args.enable_metrics)

    def test_parse_args_custom(self) -> None:
        """Test parse_args with custom CLI arguments."""
        cli_args = [
            "--protos-loc",
            "/custom/protos",
            "--xmanager-branch",
            "custom-branch",
            "--venv",
            "/custom/venv",
            "--enable-metrics",
        ]
        args = install_xmanager.parse_args(cli_args)
        self.assertEqual(args.protos_loc, "/custom/protos")
        self.assertEqual(args.xmanager_branch, "custom-branch")
        self.assertEqual(args.venv, "/custom/venv")
        self.assertTrue(args.enable_metrics)

    def test_check_prerequisites_success(self) -> None:
        """Test check_prerequisites when all binaries are found."""
        with mock.patch("shutil.which", return_value="/usr/bin/mock"):
            install_xmanager.check_prerequisites()

    def test_check_prerequisites_missing(self) -> None:
        """Test check_prerequisites when git is missing."""
        with mock.patch("shutil.which", return_value=None), self.assertRaises(SystemExit):
            install_xmanager.check_prerequisites()

    def test_check_git_version_ok(self) -> None:
        """Test check_git_version with version 2.30.0."""
        mock_res = mock.Mock(stdout="git version 2.30.0")
        with mock.patch("subprocess.run", return_value=mock_res):
            with mock.patch("sys.stderr", new_callable=io.StringIO) as stderr:
                install_xmanager.check_git_version()
                self.assertEqual(stderr.getvalue(), "")

    def test_check_git_version_macos(self) -> None:
        """Test check_git_version with macOS git version format."""
        mock_res = mock.Mock(stdout="git version 2.39.3 (Apple Git-146)")
        with mock.patch("subprocess.run", return_value=mock_res):
            with mock.patch("sys.stderr", new_callable=io.StringIO) as stderr:
                install_xmanager.check_git_version()
                self.assertEqual(stderr.getvalue(), "")

    def test_check_git_version_old(self) -> None:
        """Test check_git_version with old version 2.20.0."""
        mock_res = mock.Mock(stdout="git version 2.20.0")
        with mock.patch("subprocess.run", return_value=mock_res):
            with mock.patch("sys.stderr", new_callable=io.StringIO) as stderr:
                install_xmanager.check_git_version()
                self.assertIn("Warning: Git version is 2.20.0", stderr.getvalue())

    def test_rewrite_proto_imports(self) -> None:
        """Test rewrite_proto_imports replaces third_party path."""
        with tempfile.TemporaryDirectory() as tmp_dir_str:
            tmp_dir = Path(tmp_dir_str)
            proto_file = tmp_dir / "test.proto"
            proto_file.write_text(
                'import "third_party/xmanager_cloud/service.proto";\n',
                encoding="utf-8",
            )
            install_xmanager.rewrite_proto_imports(tmp_dir)
            updated = proto_file.read_text(encoding="utf-8")
            self.assertEqual(updated, 'import "xmanager_cloud/service.proto";\n')

    def test_retrieve_protos_local(self) -> None:
        """Test retrieve_protos with local directory containing required protos."""
        with tempfile.TemporaryDirectory() as tmp_src_str:
            with tempfile.TemporaryDirectory() as tmp_dst_str:
                src = Path(tmp_src_str)
                dst = Path(tmp_dst_str)
                (src / "experiment").mkdir()
                (src / "xid").mkdir()
                (src / "dashboard").mkdir()
                install_xmanager.retrieve_protos(str(src), dst)
                self.assertTrue((dst / "experiment").is_dir())
                self.assertTrue((dst / "xid").is_dir())
                self.assertTrue((dst / "dashboard").is_dir())

    def test_retrieve_protos_nonexistent_local(self) -> None:
        """Test retrieve_protos with non-existent local directory path."""
        with tempfile.TemporaryDirectory() as tmp_dst_str:
            dst = Path(tmp_dst_str)
            non_existent_src = dst / "does_not_exist"
            with self.assertRaises(SystemExit):
                install_xmanager.retrieve_protos(str(non_existent_src), dst)

    def test_retrieve_protos_missing_dirs(self) -> None:
        """Test retrieve_protos raises SystemExit when experiment or xid missing."""
        with tempfile.TemporaryDirectory() as tmp_src_str:
            with tempfile.TemporaryDirectory() as tmp_dst_str:
                src = Path(tmp_src_str)
                dst = Path(tmp_dst_str)
                (src / "experiment").mkdir()
                with self.assertRaises(SystemExit):
                    install_xmanager.retrieve_protos(str(src), dst)

    def test_copy_protos_to_service(self) -> None:
        """Test copy_protos_to_service places files into xmanager_cloud package."""
        with tempfile.TemporaryDirectory() as tmp_xmc_str:
            with tempfile.TemporaryDirectory() as tmp_xm_str:
                xmc_dir = Path(tmp_xmc_str)
                xm_dir = Path(tmp_xm_str)
                (xmc_dir / "xid").mkdir()
                (xmc_dir / "xid" / "file.proto").write_text("content", encoding="utf-8")
                install_xmanager.copy_protos_to_service(xmc_dir, "xid", "xid_service", xm_dir)
                target = xm_dir / "xmanager_cloud" / "xid_service" / "proto" / "file.proto"
                self.assertTrue(target.is_file())

    @mock.patch("install_xmanager.install_xmanager_package")
    @mock.patch("install_xmanager.compile_protos")
    @mock.patch("install_xmanager.retrieve_protos")
    @mock.patch("install_xmanager.clone_repositories")
    @mock.patch("install_xmanager.install_dependencies")
    @mock.patch("install_xmanager.setup_virtualenv")
    @mock.patch("install_xmanager.check_git_version")
    @mock.patch("install_xmanager.check_prerequisites")
    @mock.patch("install_xmanager.verify_imports")
    def test_main_execution(
        self,
        mock_verify: mock.Mock,
        mock_pre: mock.Mock,
        mock_git_ver: mock.Mock,
        mock_venv: mock.Mock,
        mock_deps: mock.Mock,
        mock_clones: mock.Mock,
        mock_protos: mock.Mock,
        mock_compile: mock.Mock,
        mock_install: mock.Mock,
    ) -> None:
        """Test main execution flow with mocked external operations."""
        mock_venv.return_value = Path("/mock/venv/bin/python3")

        def mock_retrieve(loc: str, dst: Path) -> None:
            (dst / "xid").mkdir(parents=True, exist_ok=True)
            (dst / "experiment").mkdir(parents=True, exist_ok=True)
            (dst / "dashboard").mkdir(parents=True, exist_ok=True)

        mock_protos.side_effect = mock_retrieve

        with tempfile.TemporaryDirectory() as tmp_venv:
            install_xmanager.main(["--venv", tmp_venv])

        mock_pre.assert_called_once()
        mock_git_ver.assert_called_once()
        mock_venv.assert_called_once()
        mock_deps.assert_called_once()
        mock_clones.assert_called_once()
        mock_protos.assert_called_once()
        mock_compile.assert_called_once()
        mock_install.assert_called_once()
        mock_verify.assert_called_once()


if __name__ == "__main__":
    unittest.main()
