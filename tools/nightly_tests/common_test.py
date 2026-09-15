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

"""Unit tests for common nightly test verification utilities."""

from __future__ import annotations

import io
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import common


class CommonTest(unittest.TestCase):
    """Test suite for tools.nightly_tests.common helper functions."""

    def test_get_flag(self) -> None:
        # None flags object returns default
        self.assertIsNone(common.get_flag(None, "xid"))
        self.assertEqual(common.get_flag(None, "xid", "default_val"), "default_val")

        # Object with attribute present
        flags_obj = SimpleNamespace(xid="42", endpoint="localhost:8080", empty=None)
        self.assertEqual(common.get_flag(flags_obj, "xid"), "42")
        self.assertEqual(common.get_flag(flags_obj, "endpoint"), "localhost:8080")

        # Object with missing attribute or None value returns default
        self.assertIsNone(common.get_flag(flags_obj, "missing"))
        self.assertEqual(common.get_flag(flags_obj, "missing", "fallback"), "fallback")
        self.assertEqual(common.get_flag(flags_obj, "empty", "fallback"), "fallback")

    def test_validate_argv(self) -> None:
        # Valid argv with 1 or 0 elements
        common.validate_argv(["script.py"])
        common.validate_argv([])

        # Extra positional arguments should raise error
        with self.assertRaises((Exception, ValueError)):
            common.validate_argv(["script.py", "extra_arg"])

        with self.assertRaises((Exception, ValueError)):
            common.validate_argv(["script.py", "arg1", "arg2"])

    def test_parse_xid(self) -> None:
        # Plain numeric or slug ID
        self.assertEqual(common.parse_xid("42"), "42")
        self.assertEqual(common.parse_xid("  12345  "), "12345")
        self.assertEqual(common.parse_xid("custom-slug"), "custom-slug")

        # Resource name format experiments/<id>
        self.assertEqual(common.parse_xid("experiments/42"), "42")
        self.assertEqual(common.parse_xid("experiments/test-exp-99"), "test-exp-99")

        # Empty / None when required=True raises ValueError
        with self.assertRaises(ValueError) as ctx:
            common.parse_xid(None, required=True)
        self.assertIn("Experiment XID (--xid) is required", str(ctx.exception))

        with self.assertRaises(ValueError) as ctx:
            common.parse_xid("", required=True)
        self.assertIn("Experiment XID (--xid) is required", str(ctx.exception))

        with self.assertRaises(ValueError) as ctx:
            common.parse_xid("   ", required=True)
        self.assertIn("Experiment XID (--xid) is required", str(ctx.exception))

        # Empty / None when required=False returns empty string
        self.assertEqual(common.parse_xid(None, required=False), "")
        self.assertEqual(common.parse_xid("", required=False), "")
        self.assertEqual(common.parse_xid("   ", required=False), "")

    def test_resolve_endpoint(self) -> None:
        # Provided endpoint takes highest precedence
        self.assertEqual(
            common.resolve_endpoint("grpc.example.com:443"),
            "grpc.example.com:443",
        )

        # Fallback to XMANAGER_ENDPOINT environment variable
        with mock.patch.dict("os.environ", {"XMANAGER_ENDPOINT": "custom-ess:9090"}):
            self.assertEqual(common.resolve_endpoint(None), "custom-ess:9090")
            self.assertEqual(common.resolve_endpoint(""), "custom-ess:9090")

        # Default fallback when no env var is set
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(common.resolve_endpoint(None), "localhost:8080")

    def test_resolve_ui_base_url(self) -> None:
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.resolve_ui_base_url",
            return_value="https://xmc-ui.example.com",
        ) as mock_resolve:
            result = common.resolve_ui_base_url()
            self.assertEqual(result, "https://xmc-ui.example.com")
            mock_resolve.assert_called_once()

    def test_ess_channel_context(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()

        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
            return_value=(mock_stub, mock_channel),
        ) as mock_create_stub:
            with common.ess_channel_context("ess-server:8080") as (stub, channel):
                mock_create_stub.assert_called_once_with("ess-server:8080")
                self.assertEqual(stub, mock_stub)
                self.assertEqual(channel, mock_channel)
                mock_channel.close.assert_not_called()

            # Verify channel was closed after context exit
            mock_channel.close.assert_called_once()

    def test_handle_verification_errors_success(self) -> None:
        # Block executes without exception
        with common.handle_verification_errors():
            pass

    def test_handle_verification_errors_assertion(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with common.handle_verification_errors():
                    raise AssertionError("WorkUnit count mismatch")
            self.assertEqual(ctx.exception.code, 1)
            self.assertIn("ASSERTION FAILED: WorkUnit count mismatch", mock_stderr.getvalue())

    def test_handle_verification_errors_timeout(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with common.handle_verification_errors():
                    raise TimeoutError("Timed out waiting for completion")
            self.assertEqual(ctx.exception.code, 1)
            self.assertIn("ASSERTION FAILED: Timed out waiting", mock_stderr.getvalue())

    def test_handle_verification_errors_generic_exception(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with common.handle_verification_errors():
                    raise RuntimeError("gRPC connection broken")
            self.assertEqual(ctx.exception.code, 2)
            self.assertIn("UNEXPECTED ERROR during verification", mock_stderr.getvalue())

    def test_print_banner_mapping(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            common.print_banner(
                "Test Banner",
                {"XID": "123", "Workers": ["w1", "w2"]},
            )
            output = mock_stdout.getvalue()
            self.assertIn("Test Banner", output)
            self.assertIn("XID", output)
            self.assertIn("123", output)
            self.assertIn("['w1', 'w2']", output)

    def test_print_banner_sequence(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            common.print_banner(
                "Test Banner Seq",
                [("Queue", "cpu-queue"), ("Count", 3)],
            )
            output = mock_stdout.getvalue()
            self.assertIn("Test Banner Seq", output)
            self.assertIn("cpu-queue", output)
            self.assertIn("Count", output)

    def test_print_success_summary_mapping(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            common.print_success_summary(
                "VERIFICATION PASSED",
                {"Status": "OK", "Total": 10},
            )
            output = mock_stdout.getvalue()
            self.assertIn("VERIFICATION PASSED", output)
            self.assertIn("Status", output)
            self.assertIn("OK", output)

    def test_print_success_summary_sequence(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            common.print_success_summary(
                "VERIFICATION PASSED SEQ",
                [("Experiment", "experiments/42"), ("Units", ("u1", "u2"))],
            )
            output = mock_stdout.getvalue()
            self.assertIn("VERIFICATION PASSED SEQ", output)
            self.assertIn("experiments/42", output)
            self.assertIn("['u1', 'u2']", output)

    def test_run_main(self) -> None:
        mock_fn = mock.MagicMock()
        with mock.patch.object(common, "app", mock.MagicMock()) as mock_app:
            common.run_main(mock_fn)
            mock_app.run.assert_called_once_with(mock_fn)

    def test_run_main_no_absl(self) -> None:
        mock_fn = mock.MagicMock()
        with mock.patch.object(common, "app", None):
            with mock.patch("sys.stderr", new_callable=io.StringIO):
                with self.assertRaises(SystemExit) as ctx:
                    common.run_main(mock_fn)
                self.assertEqual(ctx.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
