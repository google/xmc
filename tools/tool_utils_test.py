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

"""Unit tests for tool_utils module."""

import hashlib
import io
import json
import os
import sys
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path
from unittest.mock import ANY, MagicMock, patch

from tool_utils import (
    DeploymentConfig,
    HandledError,
    _bootstrap_venv,
    build_and_push_images,
    compute_helm_chart_hash,
    configure_docker_auth,
    ensure_venv,
    load_and_validate_config,
    parse_yaml_vars,
    run_tool,
)


class TestToolUtils(unittest.TestCase):
    """Test suite for tool_utils helper functions."""

    @patch.dict(os.environ, {"TEST_SRCDIR": "/mock/srcdir"}, clear=False)
    @patch("os.execv")
    def test_ensure_venv_in_bazel(self, mock_execv):
        ensure_venv()
        mock_execv.assert_not_called()

    @patch.dict(os.environ, {"XMC_BYPASS_VENV": "1"}, clear=False)
    @patch("os.execv")
    def test_ensure_venv_bypass(self, mock_execv):
        env = dict(os.environ)
        env.pop("TEST_SRCDIR", None)
        env.pop("BAZEL_TEST", None)
        with patch.dict(os.environ, env, clear=True):
            ensure_venv()
        mock_execv.assert_not_called()

    @patch("os.execv")
    def test_ensure_venv_already_active(self, mock_execv):
        env = dict(os.environ)
        env.pop("TEST_SRCDIR", None)
        env.pop("BAZEL_TEST", None)
        with (
            patch.dict(os.environ, env, clear=True),
            patch("sys.prefix", "/mock/venv"),
            patch("sys.base_prefix", "/usr"),
        ):
            ensure_venv()
        mock_execv.assert_not_called()

    @patch("tool_utils._should_bypass_venv", return_value=False)
    @patch("os.execv")
    def test_ensure_venv_reexec(self, mock_execv, mock_bypass):
        env = dict(os.environ)
        env.pop("TEST_SRCDIR", None)
        env.pop("BAZEL_TEST", None)
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            venv_py = venv_dir / "bin" / "python3"
            venv_py.parent.mkdir(parents=True, exist_ok=True)
            venv_py.touch()
            with (
                patch.dict(os.environ, env, clear=True),
                patch("sys.prefix", "/usr"),
                patch("sys.base_prefix", "/usr"),
                patch("sys.executable", "/usr/bin/python3"),
            ):
                ensure_venv(venv_dir)
            mock_execv.assert_called_once_with(str(venv_py), [str(venv_py)] + sys.argv)

    @patch("tool_utils._should_bypass_venv", return_value=False)
    @patch("tool_utils._bootstrap_venv")
    @patch("os.execv")
    def test_ensure_venv_bootstrap_and_reexec(self, mock_execv, mock_bootstrap, mock_bypass):
        env = dict(os.environ)
        env.pop("TEST_SRCDIR", None)
        env.pop("BAZEL_TEST", None)
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            venv_py = venv_dir / "bin" / "python3"

            def create_bin(_):
                venv_py.parent.mkdir(parents=True, exist_ok=True)
                venv_py.touch()

            mock_bootstrap.side_effect = create_bin
            with (
                patch.dict(os.environ, env, clear=True),
                patch("sys.prefix", "/usr"),
                patch("sys.base_prefix", "/usr"),
                patch("sys.executable", "/usr/bin/python3"),
            ):
                ensure_venv(venv_dir)
            mock_bootstrap.assert_called_once_with(venv_dir.resolve())
            mock_execv.assert_called_once_with(str(venv_py), [str(venv_py)] + sys.argv)

    @patch("tool_utils._should_bypass_venv", return_value=False)
    @patch("tool_utils._bootstrap_venv", side_effect=RuntimeError("bootstrap failed"))
    @patch("logging.exception")
    @patch("os.execv")
    def test_ensure_venv_bootstrap_fails(
        self, mock_execv, mock_log_exception, mock_bootstrap, mock_bypass
    ):
        env = dict(os.environ)
        env.pop("TEST_SRCDIR", None)
        env.pop("BAZEL_TEST", None)
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            with (
                patch.dict(os.environ, env, clear=True),
                patch("sys.prefix", "/usr"),
                patch("sys.base_prefix", "/usr"),
                patch("sys.executable", "/usr/bin/python3"),
            ):
                ensure_venv(venv_dir)
            mock_bootstrap.assert_called_once_with(venv_dir.resolve())
            mock_log_exception.assert_called_once_with("Failed to bootstrap virtual environment")
            mock_execv.assert_not_called()

    @patch("subprocess.run")
    def test_bootstrap_venv_calls_setup_script(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            with patch("pathlib.Path.is_file", return_value=True):
                _bootstrap_venv(venv_dir)
            mock_run.assert_called_once()

    @patch("subprocess.run")
    def test_bootstrap_venv_pip_index_url_fallback(self, mock_run):
        env = dict(os.environ)
        env.pop("PIP_INDEX_URL", None)
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            with (
                patch.dict(os.environ, env, clear=True),
                patch("pathlib.Path.is_file", return_value=True),
            ):
                _bootstrap_venv(venv_dir)
                self.assertEqual(os.environ.get("PIP_INDEX_URL"), "https://pypi.org/simple")

    def test_run_tool_success(self):
        """Test successful execution of tool runner."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        main_fn = MagicMock(return_value={"result": "ok"})

        captured_stdout = io.StringIO()
        with (
            patch("sys.stdout", captured_stdout),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 0
        self.assertEqual(exit_ctx.exception.code, 0)

        # Verify parser and main_fn were called
        parser.parse_args.assert_called_once()
        main_fn.assert_called_once_with(mock_args)

        # Verify stdout json
        output = json.loads(captured_stdout.getvalue())
        self.assertEqual(output["status"], "success")
        self.assertEqual(output["result"], {"result": "ok"})

    def test_run_tool_handled_error(self):
        """Test tool runner with a HandledError exception."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        def raise_handled_error(args):
            raise HandledError("something went wrong", "please fix it")

        main_fn = MagicMock(side_effect=raise_handled_error)

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 1
        self.assertEqual(exit_ctx.exception.code, 1)

        # Verify stderr json
        output = json.loads(captured_stderr.getvalue())
        self.assertEqual(output["status"], "error")
        self.assertEqual(output["error_type"], "handled")
        self.assertEqual(output["message"], "something went wrong")
        self.assertEqual(output["instruction"], "please fix it")

    def test_run_tool_unhandled_error(self):
        """Test tool runner with an unexpected Exception."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        def raise_unexpected_error(args):
            raise ValueError("unexpected value")

        main_fn = MagicMock(side_effect=raise_unexpected_error)

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 2
        self.assertEqual(exit_ctx.exception.code, 2)

        # Verify stderr json
        output = json.loads(captured_stderr.getvalue())
        self.assertEqual(output["status"], "error")
        self.assertEqual(output["error_type"], "unhandled")
        self.assertEqual(output["message"], "unexpected value")
        self.assertIn("traceback", output)


class TestConfigLoader(unittest.TestCase):
    """Test suite for config loader in tool_utils."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.config_path = Path(self.tmp_dir.name) / "test_config.yaml"
        self.valid_yaml = """vars:
  deployment_name: xmc-dev
  project_id: my-project
  environment: dev
  region: us-central1
  domain_name: example.com
  image_registry: us-central1-docker.pkg.dev/my-project/xmc
  image_tag: latest
"""
        self.valid_dict = {
            "deployment_name": "xmc-dev",
            "project_id": "my-project",
            "environment": "dev",
            "region": "us-central1",
            "domain_name": "example.com",
            "image_registry": "us-central1-docker.pkg.dev/my-project/xmc",
            "image_tag": "latest",
        }
        self.expected_config = DeploymentConfig(
            project_id="my-project",
            deployment_name="xmc-dev",
            region="us-central1",
            environment="dev",
            image_registry="us-central1-docker.pkg.dev/my-project/xmc",
            image_tag="latest",
            domain_name="example.com",
        )

    def tearDown(self):
        self.tmp_dir.cleanup()

    def test_parse_yaml_vars_success(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        vars_dict = parse_yaml_vars(self.config_path)
        self.assertEqual(vars_dict, self.valid_dict)

    def test_parse_yaml_vars_line_fallback(self):
        from tool_utils import _parse_yaml_vars_line_fallback

        yaml_content = """vars:
  key1: value1
  key2: "value2#with#hash"
  key3: 'value3#with#hash'
  key4: value4 # trailing comment
  key5: "value5" # trailing comment with quotes
  # comment line
  key6: value6 # indented key
  key7: "value7 # with space and hash"
  key8: 'value8 # with space and hash'
"""
        res = _parse_yaml_vars_line_fallback(yaml_content)
        expected = {
            "key1": "value1",
            "key2": "value2#with#hash",
            "key3": "value3#with#hash",
            "key4": "value4",
            "key5": "value5",
            "key6": "value6",
            "key7": "value7 # with space and hash",
            "key8": "value8 # with space and hash",
        }
        self.assertEqual(res, expected)

    def test_load_and_validate_config_success(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        config = load_and_validate_config(str(self.config_path))
        self.assertEqual(config, self.expected_config)

    def test_load_and_validate_config_missing_required_key(self):
        invalid_yaml = """vars:
  deployment_name: xmc-dev
  project_id: my-project
  # environment is missing
  region: us-central1
  domain_name: example.com
  image_registry: us-central1-docker.pkg.dev/my-project/xmc
  image_tag: latest
"""
        self.config_path.write_text(invalid_yaml, encoding="utf-8")
        with self.assertRaises(ValueError) as cm:
            load_and_validate_config(str(self.config_path))
        self.assertIn("Required configuration field 'environment' is missing", str(cm.exception))

    def test_load_and_validate_config_invalid_value(self):
        invalid_yaml = """vars:
  deployment_name: xmc-dev! # Invalid char
  project_id: my-project
  environment: dev
  region: us-central1
  domain_name: example.com
  image_registry: us-central1-docker.pkg.dev/my-project/xmc
  image_tag: latest
"""
        self.config_path.write_text(invalid_yaml, encoding="utf-8")
        with self.assertRaises(ValueError) as cm:
            load_and_validate_config(str(self.config_path))
        self.assertIn("value 'xmc-dev!' is invalid", str(cm.exception))

    @patch.dict(os.environ, {"PROJECT_ID": "env-project", "XMC_DEPLOYMENT_NAME": "env-deploy"})
    def test_load_and_validate_config_env_overrides(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        config = load_and_validate_config(str(self.config_path))
        self.assertEqual(config.project_id, "env-project")
        self.assertEqual(config.deployment_name, "env-deploy")
        self.assertEqual(config.environment, "dev")

    def test_load_and_validate_config_with_defaults(self):
        minimal_yaml = """vars:
  deployment_name: xmc-dev
  project_id: my-project
  domain_name: example.com
"""
        self.config_path.write_text(minimal_yaml, encoding="utf-8")
        defaults = {
            "region": "us-east1",
            "environment": "staging",
            "image_registry": "us-docker.pkg.dev/my-project/xmc",
            "image_tag": "latest",
        }
        config = load_and_validate_config(str(self.config_path), defaults=defaults)
        expected = DeploymentConfig(
            deployment_name="xmc-dev",
            project_id="my-project",
            domain_name="example.com",
            region="us-east1",
            environment="staging",
            image_registry="us-docker.pkg.dev/my-project/xmc",
            image_tag="latest",
        )
        self.assertEqual(config, expected)


class TestDockerAuthConfig(unittest.TestCase):
    """Tests for configure_docker_auth helper in tool_utils."""

    def setUp(self):
        self.base_config = DeploymentConfig(
            project_id="my-project",
            deployment_name="xmc-dev",
            region="us-central1",
            environment="dev",
            image_registry="us-central1-docker.pkg.dev/my-project/xmc",
            image_tag="latest",
        )

    @patch("tool_utils.run_command")
    def test_configure_docker_gcp(self, mock_run):
        configure_docker_auth(self.base_config)
        mock_run.assert_called_once_with(
            ["gcloud", "auth", "configure-docker", "us-central1-docker.pkg.dev", "--quiet"]
        )

    @patch("tool_utils.run_command")
    def test_configure_docker_non_gcp(self, mock_run):
        config = replace(self.base_config, image_registry="docker.io/library/ubuntu")
        configure_docker_auth(config)
        mock_run.assert_not_called()

    def test_configure_docker_missing_registry(self):
        config = replace(self.base_config, image_registry="")
        with self.assertRaises(ValueError):
            configure_docker_auth(config)


class TestBuildAndPushImages(unittest.TestCase):
    """Tests for build_and_push_images helper in tool_utils."""

    def setUp(self):
        self.valid_config = DeploymentConfig(
            project_id="my-project",
            deployment_name="xmc-dev",
            region="us-central1",
            environment="dev",
            image_registry="us-central1-docker.pkg.dev/my-project/xmc",
            image_tag="latest",
        )

    @patch("tool_utils.run_command")
    def test_build_and_push_images_default(self, mock_run):
        build_and_push_images(self.valid_config)
        self.assertEqual(mock_run.call_count, 9)
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/actuator:actuator_image_push"],
            env={
                "XMC_ENV": "dev",
                "XMC_ACR_TARGET": "us-central1-docker.pkg.dev/my-project/xmc",
                "BUILD_TAG": "latest",
            },
        )

    @patch("tool_utils.run_command")
    def test_build_and_push_images_rod_enabled(self, mock_run):
        config = replace(self.valid_config, rod_deployment_enabled=True)
        build_and_push_images(config)
        self.assertEqual(mock_run.call_count, 10)
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/rod_data_provider:rod_data_provider_image_push"],
            env=ANY,
        )

    def test_build_and_push_images_missing_registry(self):
        config = replace(self.valid_config, image_registry="")
        with self.assertRaises(ValueError):
            build_and_push_images(config)


class TestComputeHelmChartHash(unittest.TestCase):
    """Tests for compute_helm_chart_hash in tool_utils."""

    def test_non_existent_directory_returns_empty_string(self):
        self.assertEqual(compute_helm_chart_hash("/path/that/does/not/exist"), "")

    def test_file_path_returns_empty_string(self):
        with tempfile.NamedTemporaryFile() as tmp_file:
            self.assertEqual(compute_helm_chart_hash(tmp_file.name), "")

    def test_empty_directory_returns_valid_sha256_digest(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            h = compute_helm_chart_hash(tmp_dir)
            self.assertEqual(len(h), 64)
            self.assertEqual(h, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

    def test_deterministic_hashing_and_sorting(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            (base / "templates").mkdir()
            (base / "values.yaml").write_text("key: value\n", encoding="utf-8")
            (base / "templates" / "deployment.yaml").write_text(
                "kind: Deployment\n", encoding="utf-8"
            )
            (base / "Chart.yaml").write_text("name: test\n", encoding="utf-8")

            hash1 = compute_helm_chart_hash(base)
            hash2 = compute_helm_chart_hash(str(base))
            self.assertEqual(hash1, hash2)
            self.assertEqual(len(hash1), 64)

    def test_content_change_changes_hash(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            val_file = base / "values.yaml"
            val_file.write_text("version: 1\n", encoding="utf-8")
            hash1 = compute_helm_chart_hash(base)

            val_file.write_text("version: 2\n", encoding="utf-8")
            hash2 = compute_helm_chart_hash(base)
            self.assertNotEqual(hash1, hash2)

    def test_filename_change_changes_hash(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            f1 = base / "file1.txt"
            f1.write_text("content", encoding="utf-8")
            hash1 = compute_helm_chart_hash(base)

            f1.unlink()
            f2 = base / "file2.txt"
            f2.write_text("content", encoding="utf-8")
            hash2 = compute_helm_chart_hash(base)
            self.assertNotEqual(hash1, hash2)

    def test_ignores_hidden_files_and_directories(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            (base / "values.yaml").write_text("test: 123\n", encoding="utf-8")
            hash_clean = compute_helm_chart_hash(base)

            # Add hidden files and hidden directories
            (base / ".DS_Store").write_text("ignored", encoding="utf-8")
            hidden_dir = base / ".git"
            hidden_dir.mkdir()
            (hidden_dir / "config").write_text("ignored", encoding="utf-8")
            nested_hidden = base / "templates" / ".secret"
            nested_hidden.mkdir(parents=True)
            (nested_hidden / "hidden.yaml").write_text("ignored", encoding="utf-8")

            hash_with_hidden = compute_helm_chart_hash(base)
            self.assertEqual(hash_clean, hash_with_hidden)

    def test_ignores_editor_backups(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            (base / "values.yaml").write_text("test: 123\n", encoding="utf-8")
            hash_clean = compute_helm_chart_hash(base)

            # Add editor backups (*~ and #*#)
            (base / "values.yaml~").write_text("backup content", encoding="utf-8")
            (base / "#values.yaml#").write_text("emacs backup", encoding="utf-8")
            (base / "templates").mkdir()
            (base / "templates" / "service.yaml~").write_text("backup", encoding="utf-8")
            (base / "templates" / "#service.yaml#").write_text("backup", encoding="utf-8")

            hash_with_backups = compute_helm_chart_hash(base)
            self.assertEqual(hash_clean, hash_with_backups)

    def test_ignores_bytecode_and_pycache(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            (base / "values.yaml").write_text("test: 123\n", encoding="utf-8")
            hash_clean = compute_helm_chart_hash(base)

            # Add bytecode and pycache
            (base / "test.pyc").write_bytes(b"\x00\x01\x02")
            pycache = base / "__pycache__"
            pycache.mkdir()
            (pycache / "compiled.cpython-310.pyc").write_bytes(b"\x03\x04\x05")
            nested_pycache = base / "templates" / "__pycache__"
            nested_pycache.mkdir(parents=True)
            (nested_pycache / "foo.pyc").write_bytes(b"\x06\x07")

            hash_with_bytecode = compute_helm_chart_hash(base)
            self.assertEqual(hash_clean, hash_with_bytecode)

    def test_chunked_hashing_large_file(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            base = Path(tmp_dir)
            large_content = b"A" * (150 * 1024)
            (base / "large_file.bin").write_bytes(large_content)

            hash1 = compute_helm_chart_hash(base)
            self.assertEqual(len(hash1), 64)

            file_hasher = hashlib.sha256()
            file_hasher.update(b"large_file.bin")
            file_hasher.update(b"\x00")
            file_hasher.update(large_content)
            hasher = hashlib.sha256()
            hasher.update(file_hasher.digest())
            self.assertEqual(hash1, hasher.hexdigest())


if __name__ == "__main__":
    unittest.main()
