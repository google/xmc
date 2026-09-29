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

"""Unit tests for tools.nightly_tests.common module."""

from __future__ import annotations

import io
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import common


class CommonTest(unittest.TestCase):
    """Test cases for common.py utilities."""

    def test_get_flag(self) -> None:
        flags_mock = SimpleNamespace(xid="12345", endpoint=None)
        self.assertEqual(common.get_flag(flags_mock, "xid"), "12345")
        self.assertEqual(common.get_flag(flags_mock, "endpoint", "default:443"), "default:443")
        self.assertEqual(common.get_flag(flags_mock, "nonexistent", "fallback"), "fallback")
        self.assertEqual(common.get_flag(None, "xid", "fallback"), "fallback")

    def test_validate_argv(self) -> None:
        common.validate_argv(["script.py"])
        with self.assertRaises(common.app.UsageError):
            common.validate_argv(["script.py", "extra_arg"])

    def test_parse_xid(self) -> None:
        self.assertEqual(common.parse_xid("12345"), "12345")
        self.assertEqual(common.parse_xid("experiments/12345"), "12345")
        self.assertEqual(common.parse_xid("dashboards/my_dashboard"), "my_dashboard")
        self.assertEqual(common.parse_xid("experiments/42"), "42")
        self.assertEqual(common.parse_xid("/experiments/42"), "42")
        self.assertEqual(common.parse_xid("experiments/test-exp-99"), "test-exp-99")
        self.assertEqual(common.parse_xid("/experiments/test-exp-99"), "test-exp-99")
        self.assertEqual(common.parse_xid("dashboards/my_slug"), "my_slug")
        self.assertEqual(common.parse_xid("/dashboards/my_slug"), "my_slug")
        self.assertEqual(common.parse_xid("dashboards/test-dashboard-123"), "test-dashboard-123")
        self.assertEqual(common.parse_xid("/dashboards/test-dashboard-123"), "test-dashboard-123")

        with self.assertRaises(ValueError):
            common.parse_xid(None, required=True)
        with self.assertRaises(ValueError):
            common.parse_xid("", required=True)
        self.assertEqual(common.parse_xid(None, required=False), "")

    def test_resolve_endpoint(self) -> None:
        # Provided endpoint takes precedence
        self.assertEqual(common.resolve_endpoint("custom-ess:9090"), "custom-ess:9090")

        # Environment variable fallback
        with mock.patch.dict("os.environ", {"XMANAGER_ENDPOINT": "env-ess:8443"}, clear=True):
            self.assertEqual(common.resolve_endpoint(None), "env-ess:8443")
            self.assertEqual(common.resolve_endpoint(""), "env-ess:8443")

        # Default fallback when no env var is set
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(common.resolve_endpoint(None), "localhost:8080")

    def test_resolve_dashboard_endpoint(self) -> None:
        # Provided endpoint takes highest precedence
        self.assertEqual(
            common.resolve_dashboard_endpoint("https://custom-dash.example.com"),
            "https://custom-dash.example.com",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("http://custom-dash.example.com"),
            "http://custom-dash.example.com",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("localhost:8080"),
            "http://localhost:8080",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("127.0.0.1:9090"),
            "http://127.0.0.1:9090",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("dashboard.example.com"),
            "https://dashboard.example.com",
        )

        # Fallback to resolve_ui_base_url
        with mock.patch(
            "tools.nightly_tests.common.resolve_ui_base_url",
            return_value="https://xmc-ui.example.com",
        ) as mock_ui:
            self.assertEqual(common.resolve_dashboard_endpoint(None), "https://xmc-ui.example.com")
            self.assertEqual(common.resolve_dashboard_endpoint(""), "https://xmc-ui.example.com")
            self.assertEqual(mock_ui.call_count, 2)

    def test_resolve_clickhouse_endpoint(self) -> None:
        # Explicit parameter takes highest precedence
        self.assertEqual(
            common.resolve_clickhouse_endpoint("http://clickhouse.internal:8123/"),
            "http://clickhouse.internal:8123",
        )
        self.assertEqual(
            common.resolve_clickhouse_endpoint("clickhouse.internal:8123"),
            "http://clickhouse.internal:8123",
        )
        # Fallback to CLICKHOUSE_ENDPOINT
        with mock.patch.dict("os.environ", {"CLICKHOUSE_ENDPOINT": "ch-host:9000"}, clear=True):
            self.assertEqual(common.resolve_clickhouse_endpoint(None), "http://ch-host:9000")
        # Fallback to CLICKHOUSE_HOST and CLICKHOUSE_HTTP_PORT
        with mock.patch.dict(
            "os.environ",
            {"CLICKHOUSE_HOST": "10.0.0.5", "CLICKHOUSE_HTTP_PORT": "8124"},
            clear=True,
        ):
            self.assertEqual(common.resolve_clickhouse_endpoint(None), "http://10.0.0.5:8124")
        # Default fallback
        with mock.patch.dict("os.environ", {}, clear=True):
            self.assertEqual(common.resolve_clickhouse_endpoint(None), "http://127.0.0.1:8123")

    def test_resolve_ui_base_url(self) -> None:
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.resolve_ui_base_url",
            return_value="https://xmc-ui.example.com",
        ) as mock_resolve:
            result = common.resolve_ui_base_url()
            self.assertEqual(result, "https://xmc-ui.example.com")
            mock_resolve.assert_called_once()

    def test_resolve_expected_work_units(self) -> None:
        # Flag takes highest precedence
        with mock.patch.object(common, "get_flag", return_value=5):
            self.assertEqual(common.resolve_expected_work_units(3), 5)

        # Non-positive flag values fallback to env var or default
        with mock.patch.object(common, "get_flag", return_value=0):
            self.assertEqual(common.resolve_expected_work_units(3), 3)

        with mock.patch.object(common, "get_flag", return_value=-5):
            self.assertEqual(common.resolve_expected_work_units(3), 3)

        # Fallback to NUM_WORK_UNITS env var when flag is None
        with mock.patch.object(common, "get_flag", return_value=None):
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "2"}):
                self.assertEqual(common.resolve_expected_work_units(3), 2)

            # Non-positive env var values fallback to default
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "0"}):
                self.assertEqual(common.resolve_expected_work_units(3), 3)

            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "-5"}):
                self.assertEqual(common.resolve_expected_work_units(3), 3)

            # Fallback to default when env var is invalid or unset
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "invalid"}):
                self.assertEqual(common.resolve_expected_work_units(3), 3)

            with mock.patch.dict("os.environ", {}, clear=True):
                self.assertEqual(common.resolve_expected_work_units(3), 3)

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
        with mock.patch.object(common.app, "run") as mock_run:
            common.run_main(mock_fn)
            mock_run.assert_called_once_with(mock_fn)


if __name__ == "__main__":
    unittest.main()
