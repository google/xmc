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
import stat
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
            self.assertEqual(args.xmc_sdk_loc, install_xmanager.REMOTE_XMC_SDK_LOC)

    def test_get_default_xmc_sdk_loc(self) -> None:
        """XMC_SDK_LOC wins, then the local checkout, then the GitHub requirement."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            script = root / "deploy" / "cli" / "install_xmanager.py"
            local_sdk = root / "sdks" / "python" / "xmc"
            with mock.patch.dict("os.environ", {}, clear=True):
                self.assertEqual(
                    install_xmanager.get_default_xmc_sdk_loc(script),
                    install_xmanager.REMOTE_XMC_SDK_LOC,
                )
                local_sdk.mkdir(parents=True)
                (local_sdk / "pyproject.toml").write_text("", encoding="utf-8")
                self.assertEqual(
                    install_xmanager.get_default_xmc_sdk_loc(script), str(local_sdk.resolve())
                )
            with mock.patch.dict("os.environ", {"XMC_SDK_LOC": "/custom/xmc"}, clear=True):
                self.assertEqual(install_xmanager.get_default_xmc_sdk_loc(script), "/custom/xmc")

    def test_install_xmc_sdk(self) -> None:
        """The xmc SDK is pip-installed from the configured location, after `--`."""
        with mock.patch("subprocess.run") as mock_run:
            install_xmanager.install_xmc_sdk(
                Path("/mock/venv/bin/python3"), "/repo/sdks/python/xmc"
            )
        mock_run.assert_called_once_with(
            ["/mock/venv/bin/python3", "-m", "pip", "install", "--", "/repo/sdks/python/xmc"],
            check=True,
        )

    def test_parse_args_custom(self) -> None:
        """Test parse_args with custom CLI arguments."""
        cli_args = [
            "--protos-loc",
            "/custom/protos",
            "--xmanager-branch",
            "custom-branch",
            "--venv",
            "/custom/venv",
            "--xmc-sdk-loc",
            "/custom/xmc",
        ]
        args = install_xmanager.parse_args(cli_args)
        self.assertEqual(args.protos_loc, "/custom/protos")
        self.assertEqual(args.xmanager_branch, "custom-branch")
        self.assertEqual(args.venv, "/custom/venv")
        self.assertEqual(args.xmc_sdk_loc, "/custom/xmc")

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

    @mock.patch("install_xmanager.install_xmc_sdk")
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
        mock_sdk: mock.Mock,
    ) -> None:
        """Test main execution flow with mocked external operations."""
        mock_venv.return_value = Path("/mock/venv/bin/python3")

        def mock_retrieve(loc: str, dst: Path) -> None:
            (dst / "xid").mkdir(parents=True, exist_ok=True)
            (dst / "experiment").mkdir(parents=True, exist_ok=True)
            (dst / "dashboard").mkdir(parents=True, exist_ok=True)

        mock_protos.side_effect = mock_retrieve

        with tempfile.TemporaryDirectory() as tmp_venv:
            install_xmanager.main(["--venv", tmp_venv, "--xmc-sdk-loc", "/repo/sdks/python/xmc"])

        mock_pre.assert_called_once()
        mock_git_ver.assert_called_once()
        mock_venv.assert_called_once()
        mock_deps.assert_called_once()
        mock_clones.assert_called_once()
        mock_protos.assert_called_once()
        mock_compile.assert_called_once()
        mock_install.assert_called_once()
        mock_sdk.assert_called_once_with(Path("/mock/venv/bin/python3"), "/repo/sdks/python/xmc")
        mock_verify.assert_called_once()

    def test_get_venv_site_packages(self) -> None:
        """Test get_venv_site_packages queries sysconfig purelib."""
        mock_res = mock.Mock(stdout="/mock/venv/lib/python3.13/site-packages\n")
        with mock.patch("subprocess.run", return_value=mock_res) as mock_run:
            result = install_xmanager.get_venv_site_packages(Path("/mock/venv/bin/python3"))
        self.assertEqual(result, Path("/mock/venv/lib/python3.13/site-packages"))
        mock_run.assert_called_once_with(
            [
                "/mock/venv/bin/python3",
                "-c",
                "import sysconfig; print(sysconfig.get_path('purelib'))",
            ],
            capture_output=True,
            text=True,
            check=True,
        )

    def test_build_bazel_dashboard_protos(self) -> None:
        """Test build_bazel_dashboard_protos builds targets and resolves bazel-bin."""
        repo_root = Path("/mock/repo")
        mock_info = mock.Mock(stdout="/mock/bazel-out/k8-fastbuild/bin\n")
        with (
            mock.patch("shutil.which", return_value="/usr/bin/bazel"),
            mock.patch("subprocess.run", side_effect=[mock.Mock(), mock_info]) as mock_run,
        ):
            result = install_xmanager.build_bazel_dashboard_protos(repo_root)
        self.assertEqual(
            result,
            Path("/mock/bazel-out/k8-fastbuild/bin/api/protos/dashboard/_virtual_imports"),
        )
        self.assertEqual(mock_run.call_count, 2)
        mock_run.assert_any_call(
            ["bazel", "build", *install_xmanager.DASHBOARD_BAZEL_TARGETS],
            cwd=repo_root,
            check=True,
        )
        mock_run.assert_any_call(
            ["bazel", "info", "bazel-bin"],
            cwd=repo_root,
            capture_output=True,
            text=True,
            check=True,
        )

    def test_build_bazel_dashboard_protos_missing_bazel_exits(self) -> None:
        """Test build_bazel_dashboard_protos exits when bazel is not in PATH."""
        with mock.patch("shutil.which", return_value=None), self.assertRaises(SystemExit):
            install_xmanager.build_bazel_dashboard_protos(Path("/mock/repo"))

    def test_copy_virtual_imports_to_site_packages(self) -> None:
        """Test copying read-only Bazel virtual imports and preserving sibling packages."""
        with tempfile.TemporaryDirectory() as tmp_str:
            tmp_dir = Path(tmp_str)
            virtual_imports = tmp_dir / "_virtual_imports"
            site_packages = tmp_dir / "site-packages"

            for target, filename in (
                ("dashboard_messages_proto", "messages_pb2.py"),
                ("dashboard_service_proto", "dashboard_service_pb2.py"),
            ):
                xm_dir = virtual_imports / target / "xmanager_cloud"
                svc_dir = xm_dir / "dashboard_service"
                proto_dir = svc_dir / "proto"
                proto_dir.mkdir(parents=True)
                proto_file = proto_dir / filename
                proto_file.write_text("# generated", encoding="utf-8")
                proto_file.chmod(stat.S_IRUSR)
                proto_dir.chmod(stat.S_IRUSR | stat.S_IXUSR)
                svc_dir.chmod(stat.S_IRUSR | stat.S_IXUSR)
                xm_dir.chmod(stat.S_IRUSR | stat.S_IXUSR)

            sibling_file = (
                site_packages
                / "xmanager_cloud"
                / "experiment_state_server"
                / "proto"
                / "api_pb2.py"
            )
            sibling_file.parent.mkdir(parents=True)
            sibling_file.write_text("# sibling", encoding="utf-8")
            sibling_file.chmod(stat.S_IRUSR)

            stale_dir = site_packages / "xmanager_cloud" / "dashboard_service" / "proto"
            stale_dir.mkdir(parents=True)
            stale_file = stale_dir / "obsolete_pb2.py"
            stale_file.write_text("# stale", encoding="utf-8")
            stale_file.chmod(stat.S_IRUSR)

            try:
                install_xmanager.copy_virtual_imports_to_site_packages(
                    virtual_imports, site_packages
                )
                install_xmanager.copy_virtual_imports_to_site_packages(
                    virtual_imports, site_packages
                )

                dest_proto = site_packages / "xmanager_cloud" / "dashboard_service" / "proto"
                self.assertFalse((dest_proto / "obsolete_pb2.py").exists())
                self.assertTrue((dest_proto / "messages_pb2.py").is_file())
                self.assertTrue((dest_proto / "dashboard_service_pb2.py").is_file())
                self.assertTrue(sibling_file.is_file())
                self.assertFalse(bool(sibling_file.stat().st_mode & stat.S_IWUSR))
                self.assertTrue(
                    bool((dest_proto / "messages_pb2.py").stat().st_mode & stat.S_IWUSR)
                )
            finally:
                install_xmanager._make_tree_user_writable(virtual_imports)
                install_xmanager._make_tree_user_writable(site_packages)

    def test_copy_virtual_imports_missing_target_exits(self) -> None:
        """Test copy_virtual_imports_to_site_packages exits when virtual imports are missing."""
        with tempfile.TemporaryDirectory() as tmp_str:
            tmp_dir = Path(tmp_str)
            install_xmanager._make_tree_user_writable(tmp_dir / "non_existent")
            with self.assertRaises(SystemExit):
                install_xmanager.copy_virtual_imports_to_site_packages(
                    tmp_dir / "missing", tmp_dir / "site-packages"
                )

    @mock.patch("install_xmanager.copy_virtual_imports_to_site_packages")
    @mock.patch("install_xmanager.get_venv_site_packages")
    @mock.patch("install_xmanager.build_bazel_dashboard_protos")
    def test_install_bazel_dashboard_protos(
        self,
        mock_build: mock.Mock,
        mock_site: mock.Mock,
        mock_copy: mock.Mock,
    ) -> None:
        """Test install_bazel_dashboard_protos coordinates build and copy."""
        mock_build.return_value = Path("/mock/virtual_imports")
        mock_site.return_value = Path("/mock/site-packages")
        venv_python = Path("/mock/venv/bin/python3")
        repo_root = Path("/mock/repo")

        install_xmanager.install_bazel_dashboard_protos(venv_python, repo_root)

        mock_build.assert_called_once_with(repo_root)
        mock_site.assert_called_once_with(venv_python)
        mock_copy.assert_called_once_with(
            Path("/mock/virtual_imports"), Path("/mock/site-packages")
        )

    @mock.patch("venv.create")
    def test_setup_virtualenv_creates_when_missing_python(
        self,
        mock_create: mock.Mock,
    ) -> None:
        """Test setup_virtualenv runs venv.create when python binary is absent."""
        with tempfile.TemporaryDirectory() as tmp_str:
            venv_dir = Path(tmp_str)
            result = install_xmanager.setup_virtualenv(venv_dir)
            mock_create.assert_called_once_with(venv_dir, with_pip=True)
            self.assertEqual(result, venv_dir / "bin" / "python")

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

    @mock.patch("install_xmanager.install_bazel_dashboard_protos")
    @mock.patch("install_xmanager.setup_virtualenv")
    @mock.patch("install_xmanager.check_prerequisites")
    def test_main_dashboard_protos_only(
        self,
        mock_pre: mock.Mock,
        mock_venv: mock.Mock,
        mock_install_dashboard: mock.Mock,
    ) -> None:
        """Test main with --dashboard-protos-only skips full clone and installs protos."""
        mock_venv.return_value = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_venv:
            install_xmanager.main(["--venv", tmp_venv, "--dashboard-protos-only"])
        mock_venv.assert_called_once()
        mock_install_dashboard.assert_called_once()
        mock_pre.assert_not_called()


if __name__ == "__main__":
    unittest.main()
