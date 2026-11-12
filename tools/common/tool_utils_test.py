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

import argparse
import io
import json
import os
import shlex
import subprocess
import sys
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path
from types import MappingProxyType
from typing import ClassVar
from unittest.mock import ANY, MagicMock, patch

from tools.common.tool_utils import (
    DeploymentConfig,
    HandledError,
    _bootstrap_venv,
    build_and_push_images,
    configure_docker_auth,
    deploy_component_profile,
    ensure_venv,
    load_and_validate_config,
    log_info,
    parse_bool_flag,
    parse_positive_int,
    parse_semver_prefix,
    parse_yaml_vars,
    positive_int_arg,
    run_command,
    run_tool,
    verify_helm_min_version,
)


class TestPositiveIntParsing(unittest.TestCase):
    """Tests for parse_positive_int and its argparse adapter."""

    _VALID = ((1, 1), (15, 15), ("15", 15), (" 7\n", 7), ("007", 7))
    _INVALID = (0, -3, True, False, 1.0, None, "", " ", "0", "-3", "+5", "1_000", "1.5", "abc", "²")

    def test_parse_positive_int_accepts_positive_integers(self):
        """Ints >= 1 and ASCII digit strings (whitespace-trimmed) are accepted."""
        for value, want in self._VALID:
            with self.subTest(value=value):
                self.assertEqual(parse_positive_int(value), want)

    def test_parse_positive_int_rejects_everything_else(self):
        """Bools, floats, non-positive, signed, and non-ASCII values return None."""
        for value in self._INVALID:
            with self.subTest(value=value):
                self.assertIsNone(parse_positive_int(value))

    def test_positive_int_arg_matches_parse_positive_int(self):
        """The argparse adapter accepts and rejects the same strings."""
        self.assertEqual(positive_int_arg(" 7\n"), 7)
        for value in (v for v in self._INVALID if isinstance(v, str)):
            with self.subTest(value=value):
                with self.assertRaisesRegex(argparse.ArgumentTypeError, "integer >= 1"):
                    positive_int_arg(value)


class TestToolUtils(unittest.TestCase):
    """Test suite for tool_utils helper functions."""

    @patch.dict(os.environ, {"TEST_SRCDIR": "/mock/srcdir"}, clear=False)
    @patch("os.execv")
    def test_ensure_venv_in_bazel(self, mock_execv):
        ensure_venv()
        mock_execv.assert_not_called()

    @patch("tools.common.tool_utils._bootstrap_venv")
    @patch("os.execv")
    def test_ensure_venv_bypasses_on_help_flag(self, mock_execv, mock_bootstrap):
        modules = {k: v for k, v in sys.modules.items() if k not in ("unittest", "pytest")}
        with (
            tempfile.TemporaryDirectory() as tmp_dir,
            patch.dict(os.environ, {}, clear=True),
            patch.dict(sys.modules, modules, clear=True),
            patch("sys.prefix", "/usr"),
            patch("sys.base_prefix", "/usr"),
            patch("tools.common.tool_utils.__file__", "/repo/tools/common/tool_utils.py"),
            patch("pathlib.Path.cwd", return_value=Path("/repo")),
        ):
            venv_dir = Path(tmp_dir) / "venv"
            for flag in ("-h", "--help"):
                with self.subTest(flag=flag), patch.object(sys, "argv", ["tool.py", flag]):
                    ensure_venv(venv_dir)
            mock_bootstrap.assert_not_called()
            mock_execv.assert_not_called()

            with patch.object(sys, "argv", ["tool.py"]):
                ensure_venv(venv_dir)
            mock_bootstrap.assert_called_once_with(venv_dir.resolve())

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

    @patch("tools.common.tool_utils._should_bypass_venv", return_value=False)
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

    @patch("tools.common.tool_utils._should_bypass_venv", return_value=False)
    @patch("tools.common.tool_utils._bootstrap_venv")
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

    @patch("tools.common.tool_utils._should_bypass_venv", return_value=False)
    @patch("tools.common.tool_utils._bootstrap_venv", side_effect=RuntimeError("bootstrap failed"))
    @patch("tools.common.tool_utils._LOGGER.exception")
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
    def test_bootstrap_venv_sends_setup_output_to_stderr(self, mock_run):
        """Setup and pip output must not mix with the JSON that tools print to stdout."""
        with (
            tempfile.TemporaryDirectory() as tmp_dir,
            patch("pathlib.Path.is_file", return_value=True),
        ):
            _bootstrap_venv(Path(tmp_dir) / "venv")
        self.assertIs(mock_run.call_args.kwargs["stdout"], sys.stderr)

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
        self.assertNotIn("details", output)

    def test_run_tool_handled_error_with_details(self):
        """Test that HandledError details, any Mapping, are added to the error JSON."""
        details = MappingProxyType({"failed_targets": [{"target": "//a:a_test"}]})
        main_fn = MagicMock(side_effect=HandledError("tests failed", "fix them", details))

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(MagicMock(), main_fn)

        self.assertEqual(exit_ctx.exception.code, 1)
        self.assertEqual(json.loads(captured_stderr.getvalue())["details"], dict(details))

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

    def test_run_tool_keyboard_interrupt(self):
        """Ctrl-C prints the interrupted error JSON, not a traceback, and exits 130."""
        main_fn = MagicMock(side_effect=KeyboardInterrupt)

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(MagicMock(), main_fn)

        self.assertEqual(exit_ctx.exception.code, 130)
        output = json.loads(captured_stderr.getvalue())
        self.assertEqual(output["status"], "error")
        self.assertEqual(output["error_type"], "interrupted")
        self.assertIn("Interrupted", output["message"])
        self.assertIn("instruction", output)
        self.assertNotIn("traceback", output)

    def test_run_tool_keyboard_interrupt_during_venv_bootstrap(self):
        """Ctrl-C while the tooling venv bootstraps is also reported as JSON."""
        main_fn = MagicMock()

        captured_stderr = io.StringIO()
        with (
            patch("tools.common.tool_utils.ensure_venv", side_effect=KeyboardInterrupt),
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(MagicMock(), main_fn)

        self.assertEqual(exit_ctx.exception.code, 130)
        self.assertEqual(json.loads(captured_stderr.getvalue())["error_type"], "interrupted")
        main_fn.assert_not_called()


class TestLogStreams(unittest.TestCase):
    """Tests for the stream parameter of the logging and command helpers."""

    def test_log_info_writes_to_stdout_unless_given_a_stream(self):
        stdout, stream = io.StringIO(), io.StringIO()
        with patch("sys.stdout", stdout):
            log_info("info for stdout")
            log_info("info for stream", stream=stream)
        self.assertIn("[INFO] info for stdout", stdout.getvalue())
        self.assertNotIn("for stream", stdout.getvalue())
        self.assertIn("[INFO] info for stream", stream.getvalue())

    def test_run_command_sends_log_and_stdout_to_stream(self):
        stdout = io.StringIO()
        with tempfile.TemporaryFile(mode="w+", encoding="utf-8") as stream:
            with patch("sys.stdout", stdout):
                run_command(
                    [sys.executable, "-c", "print('streamed subprocess output')"],
                    stream=stream,
                )
            stream.seek(0)
            stream_output = stream.read()
        self.assertIn("Running:", stream_output)
        self.assertIn("streamed subprocess output", stream_output)
        self.assertEqual(stdout.getvalue(), "")

    @patch("subprocess.run")
    def test_run_command_keeps_default_stdout_and_captured_pipes(self, mock_run):
        with patch("sys.stdout", io.StringIO()):
            run_command(["git", "status"])
        run_command(["git", "status"], capture_output=True, log_cmd=False, stream=io.StringIO())
        default_call, captured_call = mock_run.call_args_list
        self.assertIsNone(default_call.kwargs["stdout"])
        self.assertIsNone(captured_call.kwargs["stdout"])
        self.assertTrue(captured_call.kwargs["capture_output"])


class TestRunCommandHardening(unittest.TestCase):
    """Tests for run_command stdin, timeout, decoding, and HandledError options."""

    _BAD_BYTES: ClassVar[list[str]] = [
        sys.executable,
        "-c",
        "import sys; sys.stdout.buffer.write(b'ok\\xff')",
    ]

    def _run(self, cmd: list[str], **kwargs):
        return run_command(cmd, capture_output=True, log_cmd=False, log_on_error=False, **kwargs)

    def test_input_text_is_written_to_stdin(self):
        cmd = [sys.executable, "-c", "import sys; print(sys.stdin.read().upper())"]
        self.assertEqual(self._run(cmd, input_text="abc").stdout.strip(), "ABC")

    def test_errors_replace_tolerates_undecodable_output(self):
        self.assertEqual(self._run(self._BAD_BYTES, errors="replace").stdout, "ok\ufffd")

    def test_default_decoding_stays_strict(self):
        with self.assertRaises(UnicodeDecodeError):
            self._run(self._BAD_BYTES)

    def test_raw_exceptions_without_handled_instruction(self):
        with self.assertRaises(FileNotFoundError):
            self._run(["xmc-nonexistent-binary"])
        with self.assertRaises(subprocess.TimeoutExpired):
            self._run([sys.executable, "-c", "import time; time.sleep(10)"], timeout=0.2)
        with self.assertRaises(subprocess.CalledProcessError):
            self._run([sys.executable, "-c", "raise SystemExit(3)"])

    def test_missing_binary_raises_handled_error(self):
        with self.assertRaisesRegex(HandledError, "Cannot run xmc-nonexistent-binary") as ctx:
            self._run(["xmc-nonexistent-binary"], handled_instruction="Install it.")
        self.assertEqual(ctx.exception.instruction, "Install it.")
        self.assertIsInstance(ctx.exception.__cause__, FileNotFoundError)

    def test_timeout_raises_handled_error(self):
        cmd = [sys.executable, "-c", "import time; time.sleep(10)"]
        with self.assertRaisesRegex(HandledError, r"timed out after 0\.2s"):
            self._run(cmd, timeout=0.2, handled_instruction="Retry.")

    def test_non_zero_exit_raises_handled_error_with_stderr(self):
        cmd = [sys.executable, "-c", "import sys; sys.exit('boom')"]
        with self.assertRaisesRegex(HandledError, r"exit code 1: .*\nboom") as ctx:
            self._run(cmd, handled_instruction="Fix it.")
        self.assertEqual(ctx.exception.instruction, "Fix it.")
        self.assertIn(shlex.quote(cmd[2]), ctx.exception.message)

    def test_non_zero_exit_without_check_returns_result(self):
        cmd = [sys.executable, "-c", "raise SystemExit(3)"]
        result = self._run(cmd, check=False, handled_instruction="Unused.")
        self.assertEqual(result.returncode, 3)

    @patch("tools.common.tool_utils.log_error")
    def test_handled_failure_still_logs_when_requested(self, mock_log_error):
        cmd = [sys.executable, "-c", "raise SystemExit(3)"]
        with self.assertRaises(HandledError):
            run_command(cmd, capture_output=True, log_cmd=False, handled_instruction="Fix it.")
        mock_log_error.assert_called_once()


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
        from tools.common.tool_utils import _parse_yaml_vars_line_fallback

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

    def test_load_and_validate_config_log_monitor_flag_yaml(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        default_config = load_and_validate_config(str(self.config_path))
        self.assertTrue(default_config.log_monitor_deployment_enabled)

        yaml_without_log_monitor = self.valid_yaml + "\n  log_monitor_deployment_enabled: false\n"
        self.config_path.write_text(yaml_without_log_monitor, encoding="utf-8")
        config = load_and_validate_config(str(self.config_path))
        self.assertFalse(config.log_monitor_deployment_enabled)

    @patch.dict(os.environ, {"LOG_MONITOR_DEPLOYMENT_ENABLED": "false"})
    def test_load_and_validate_config_log_monitor_flag_env(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        config = load_and_validate_config(str(self.config_path))
        self.assertFalse(config.log_monitor_deployment_enabled)

    def test_load_and_validate_config_database_backend_yaml_and_validation(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        self.assertEqual(
            load_and_validate_config(str(self.config_path)).database_backend, "spanner"
        )

        self.config_path.write_text(
            self.valid_yaml + "\n  database_backend: postgres\n", encoding="utf-8"
        )
        self.assertEqual(
            load_and_validate_config(str(self.config_path)).database_backend, "postgres"
        )

        self.config_path.write_text(
            self.valid_yaml + "\n  database_backend: mysql\n", encoding="utf-8"
        )
        with self.assertRaises(ValueError) as cm:
            load_and_validate_config(str(self.config_path))
        self.assertIn("database_backend", str(cm.exception))

    @patch.dict(os.environ, {"DB_BACKEND": "postgres"})
    def test_load_and_validate_config_database_backend_env(self):
        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        config = load_and_validate_config(str(self.config_path))
        self.assertEqual(config.database_backend, "postgres")

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

    @patch.dict(os.environ, {}, clear=True)
    def test_load_and_validate_config_rejects_disabled_components_yaml_and_env(self):
        self.config_path.write_text(
            self.valid_yaml + "\n  enable_components: false\n", encoding="utf-8"
        )
        with self.assertRaisesRegex(ValueError, "enable_components=false is no longer supported"):
            load_and_validate_config(str(self.config_path))

        self.config_path.write_text(
            self.valid_yaml + "\n  enable_components: true\n", encoding="utf-8"
        )
        self.assertEqual(load_and_validate_config(str(self.config_path)).project_id, "my-project")

        with patch.dict(os.environ, {"ENABLE_COMPONENTS": "false"}):
            with self.assertRaisesRegex(
                ValueError, "enable_components=false is no longer supported"
            ):
                load_and_validate_config(str(self.config_path))

    @patch.dict(os.environ, {}, clear=True)
    def test_load_and_validate_config_rejects_retired_metrics_keys(self):
        """Retired 1.0.0 keys fail closed instead of being silently dropped by the blueprint."""
        for retired_key in (
            "enable_metrics: true",
            "enable_metrics: false",
            "ingestion_gateway_deployment_enabled: true",
            "clickhouse_host: ch",
            'clickhouse_port: "9000"',
            "clickhouse_database: default",
            "clickhouse_user: default",
        ):
            with self.subTest(retired_key=retired_key):
                self.config_path.write_text(
                    self.valid_yaml + f"\n  {retired_key}\n", encoding="utf-8"
                )
                with self.assertRaisesRegex(ValueError, "retired in XMC 1.0.0"):
                    load_and_validate_config(str(self.config_path))

        self.config_path.write_text(self.valid_yaml, encoding="utf-8")
        self.assertEqual(load_and_validate_config(str(self.config_path)).project_id, "my-project")


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

    @patch("tools.common.tool_utils.run_command")
    def test_configure_docker_gcp(self, mock_run):
        configure_docker_auth(self.base_config)
        mock_run.assert_called_once_with(
            ["gcloud", "auth", "configure-docker", "us-central1-docker.pkg.dev", "--quiet"]
        )

    @patch("tools.common.tool_utils.run_command")
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

    @patch("tools.common.tool_utils.run_command")
    def test_build_and_push_images_default(self, mock_run):
        build_and_push_images(self.valid_config)
        self.assertEqual(mock_run.call_count, 12)
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/actuator:actuator_image_push"],
            env={
                "XMC_ENV": "dev",
                "XMC_ACR_TARGET": "us-central1-docker.pkg.dev/my-project/xmc",
                "BUILD_TAG": "latest",
            },
        )
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/xmc_bridge:xmc_bridge_image_push"],
            env=ANY,
        )
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/study_controller:study_controller_image_push"],
            env=ANY,
        )
        mock_run.assert_any_call(
            ["bazel", "run", "//cmd/rod_data_provider:rod_data_provider_image_push"],
            env=ANY,
        )

    def test_build_and_push_images_missing_registry(self):
        config = replace(self.valid_config, image_registry="")
        with self.assertRaises(ValueError):
            build_and_push_images(config)


class TestComponentProfileAndFlags(unittest.TestCase):
    """Tests for parse_bool_flag and deploy_component_profile."""

    def setUp(self):
        self.base_config = DeploymentConfig(
            project_id="my-project",
            deployment_name="xmc-dev",
            region="us-central1",
            environment="dev",
            image_registry="us-central1-docker.pkg.dev/my-project/xmc",
            image_tag="latest",
        )

    def test_parse_bool_flag(self):
        self.assertTrue(parse_bool_flag(True))
        self.assertFalse(parse_bool_flag(False))
        self.assertTrue(parse_bool_flag("true"))
        self.assertTrue(parse_bool_flag("1"))
        self.assertTrue(parse_bool_flag("YES"))
        self.assertFalse(parse_bool_flag("false"))
        self.assertFalse(parse_bool_flag("0"))
        self.assertFalse(parse_bool_flag(None))

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_default(self, mock_run):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="not found"),
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="not found"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        ]
        deploy_component_profile(self.base_config)
        self.assertEqual(mock_run.call_count, 3)
        cmd = mock_run.call_args[0][0]
        self.assertEqual(
            cmd[:5],
            ["helm", "upgrade", "--install", "--reset-then-reuse-values", "xmc-profile"],
        )
        self.assertTrue(cmd[5].endswith("deploy/profiles/stable-2026a"))
        self.assertEqual(cmd[6:], ["-n", "xmc", "--wait"])

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_with_values_file(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp_dir:
            values_file = Path(tmp_dir) / "profile-values.yaml"
            values_file.write_text("components: {}\n", encoding="utf-8")
            mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout="not-json", stderr=""),
                subprocess.CompletedProcess(args=[], returncode=0, stdout="not-json", stderr=""),
                subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
            ]
            cfg = replace(
                self.base_config,
                namespace="tenant-a",
                profile_values=str(values_file),
            )
            deploy_component_profile(cfg)
            cmd = mock_run.call_args[0][0]
            self.assertEqual(cmd[6:], ["-f", str(values_file), "-n", "tenant-a", "--wait"])

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_unprotects_only_retired_disabled_crs(self, mock_run):
        cluster_list = json.dumps(
            {
                "items": [
                    {
                        "metadata": {
                            "name": "vizier",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"enabled": False, "deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "hpo",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"enabled": True, "deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "missing-enabled",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "--flag-injection",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"enabled": False, "deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "clickhouse",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"enabled": False, "deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "custom-retired",
                            "annotations": {"meta.helm.sh/release-name": "other-release"},
                        },
                        "spec": {"enabled": False, "deletionProtection": True},
                    },
                    {
                        "metadata": {
                            "name": "already-unprotected",
                            "annotations": {"meta.helm.sh/release-name": "xmc-profile"},
                        },
                        "spec": {"enabled": False, "deletionProtection": False},
                    },
                ]
            }
        )
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=cluster_list, stderr=""),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="not found"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        ]
        deploy_component_profile(self.base_config)
        self.assertEqual(mock_run.call_count, 4)
        patch_cmd = mock_run.call_args_list[1][0][0]
        self.assertEqual(
            patch_cmd,
            [
                "kubectl",
                "patch",
                "-n",
                "xmc",
                "--type=merge",
                "-p",
                '{"spec":{"deletionProtection":false}}',
                "--",
                "components.xmc.dev",
                "vizier",
            ],
        )

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_sanitizes_retired_disabled_helm_values(self, mock_run):
        stored_values = json.dumps(
            {
                "components": {
                    "clickhouse": {"enabled": True},
                    "vizier": {"enabled": False},
                    "hpo": {"enabled": True},
                },
                "global": {"env": "dev"},
            }
        )
        captured_sanitized: dict[str, object] = {}

        def fake_run(cmd, **_kwargs):
            if cmd[:2] == ["kubectl", "get"]:
                return subprocess.CompletedProcess(args=cmd, returncode=1, stdout="", stderr="")
            if cmd[:3] == ["helm", "get", "values"]:
                return subprocess.CompletedProcess(
                    args=cmd, returncode=0, stdout=stored_values, stderr=""
                )
            sanitized_idx = cmd.index("-f") + 1
            captured_sanitized.update(json.loads(Path(cmd[sanitized_idx]).read_text("utf-8")))
            return subprocess.CompletedProcess(args=cmd, returncode=0, stdout="", stderr="")

        mock_run.side_effect = fake_run
        deploy_component_profile(self.base_config)
        upgrade_cmd = mock_run.call_args[0][0]
        self.assertEqual(
            upgrade_cmd[:5],
            ["helm", "upgrade", "--install", "--reset-values", "xmc-profile"],
        )
        self.assertEqual(
            captured_sanitized,
            {
                "components": {
                    "clickhouse": {"enabled": True},
                    "hpo": {"enabled": True},
                },
                "global": {"env": "dev"},
            },
        )

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_sanitizes_before_profile_values(self, mock_run):
        stored_values = json.dumps(
            {"components": {"clickhouse": {"enabled": True}, "vizier": {"enabled": False}}}
        )
        with tempfile.TemporaryDirectory() as tmp_dir:
            values_file = Path(tmp_dir) / "profile-values.yaml"
            values_file.write_text("components: {}\n", encoding="utf-8")
            captured_sanitized: dict[str, object] = {}

            def fake_run(cmd, **_kwargs):
                if cmd[:2] == ["kubectl", "get"]:
                    return subprocess.CompletedProcess(args=cmd, returncode=1, stdout="", stderr="")
                if cmd[:3] == ["helm", "get", "values"]:
                    return subprocess.CompletedProcess(
                        args=cmd, returncode=0, stdout=stored_values, stderr=""
                    )
                sanitized_idx = cmd.index("-f") + 1
                captured_sanitized.update(json.loads(Path(cmd[sanitized_idx]).read_text("utf-8")))
                return subprocess.CompletedProcess(args=cmd, returncode=0, stdout="", stderr="")

            mock_run.side_effect = fake_run
            cfg = replace(self.base_config, namespace="tenant-a", profile_values=str(values_file))
            deploy_component_profile(cfg)
            upgrade_cmd = mock_run.call_args[0][0]
            self.assertEqual(
                upgrade_cmd[:5],
                ["helm", "upgrade", "--install", "--reset-values", "xmc-profile"],
            )
            flag_files = [
                upgrade_cmd[i + 1] for i, arg in enumerate(upgrade_cmd[:-1]) if arg == "-f"
            ]
            self.assertEqual(len(flag_files), 2)
            self.assertTrue(flag_files[0].endswith("/sanitized-values.json"))
            self.assertEqual(
                upgrade_cmd[6:],
                ["-f", flag_files[0], "-f", str(values_file), "-n", "tenant-a", "--wait"],
            )
            self.assertEqual(captured_sanitized, {"components": {"clickhouse": {"enabled": True}}})

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_handles_missing_cli_and_empty_catalog(self, mock_run):
        mock_run.side_effect = [
            FileNotFoundError("kubectl"),
            FileNotFoundError("helm"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        ]
        deploy_component_profile(self.base_config)
        self.assertEqual(mock_run.call_count, 3)

        with tempfile.TemporaryDirectory() as empty_chart_dir:
            mock_run.reset_mock()
            mock_run.side_effect = None
            mock_run.return_value = subprocess.CompletedProcess(
                args=[], returncode=0, stdout="", stderr=""
            )
            with patch(
                "tools.common.tool_utils._resolve_default_profile_chart",
                return_value=Path(empty_chart_dir),
            ):
                deploy_component_profile(self.base_config)
            self.assertEqual(mock_run.call_count, 1)

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_ignores_non_mapping_json_payloads(self, mock_run):
        for payload in ("null", "[]", '{"items": "not-a-list"}'):
            mock_run.reset_mock()
            mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout=payload, stderr=""),
                subprocess.CompletedProcess(args=[], returncode=0, stdout=payload, stderr=""),
                subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
            ]
            deploy_component_profile(self.base_config)
            self.assertEqual(mock_run.call_count, 3)

    @patch("tools.common.tool_utils.run_command")
    def test_deploy_component_profile_missing_values_file(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp_dir:
            cfg = replace(
                self.base_config,
                profile_values=str(Path(tmp_dir) / "missing-profile-values.yaml"),
            )
            with self.assertRaises(FileNotFoundError):
                deploy_component_profile(cfg)
            mock_run.assert_not_called()

    def test_parse_semver_prefix(self):
        self.assertEqual(parse_semver_prefix("v3.14.2+g0a1b2c"), (3, 14, 2))
        self.assertEqual(parse_semver_prefix("v3.14"), (3, 14, 0))
        self.assertEqual(parse_semver_prefix("3"), (3, 0, 0))
        self.assertEqual(parse_semver_prefix("v3.14-rc1"), (3, 14, 0))
        self.assertEqual(parse_semver_prefix("v3.14+build2"), (3, 14, 0))
        with self.assertRaises(ValueError):
            parse_semver_prefix("invalid")

    @patch("tools.common.tool_utils.run_command")
    def test_verify_helm_min_version(self, mock_run):
        mock_run.return_value = MagicMock(stdout="v3.15.0\n")
        self.assertEqual(verify_helm_min_version(), "v3.15.0")
        mock_run.return_value = MagicMock(stdout="v3.13.3\n")
        with self.assertRaises(RuntimeError):
            verify_helm_min_version()


if __name__ == "__main__":
    unittest.main()
