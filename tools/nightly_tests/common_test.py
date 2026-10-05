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
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any
from unittest import mock

from tools.nightly_tests import common, xmc_entities_validator


def _make_fake_wu(
    xid: str,
    wu_id: int,
    job_names: Sequence[str],
    queue: str = "cpu-queue",
    state: int = xmc_entities_validator.STATE_COMPLETED,
) -> SimpleNamespace:
    """Builds a stub-level fake WorkUnit protobuf namespace for orchestration tests."""
    jobs = [
        SimpleNamespace(
            name=name,
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name=name),
        )
        for name in job_names
    ]
    executable = SimpleNamespace(
        jobset=SimpleNamespace(jobs=jobs),
        scheduling_constraints=SimpleNamespace(queue_name=queue),
        HasField=lambda f: f in ("jobset", "scheduling_constraints"),
    )
    execution = SimpleNamespace(
        status=SimpleNamespace(state=state),
        HasField=lambda f: f == "status",
    )
    return SimpleNamespace(
        name=f"experiments/{xid}/workUnits/{wu_id}",
        executable=executable,
        current_work_unit_execution=execution,
        HasField=lambda f: f == "current_work_unit_execution",
    )


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

    def test_assert_experiment(self) -> None:
        mock_stub = mock.MagicMock()
        mock_exp = SimpleNamespace(name="experiments/10", title="Test Exp")
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_experiment",
            return_value=mock_exp,
        ) as mock_val:
            res = common.assert_experiment(mock_stub, "10", "Test")
            self.assertEqual(res, mock_exp)
            mock_val.assert_called_once_with(
                stub=mock_stub, xid="10", expected_title_pattern="Test"
            )

    def test_assert_work_units(self) -> None:
        mock_stub = mock.MagicMock()
        wu_list = [{"id": "1", "worker_name": "w1", "queue": "cpu-queue", "state": "COMPLETED"}]
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_work_units",
            return_value=wu_list,
        ) as mock_val:
            res = common.assert_work_units(mock_stub, "10", 1, "cpu-queue", ["w1"], 60)
            self.assertEqual(res, wu_list)
            mock_val.assert_called_once_with(
                stub=mock_stub,
                xid="10",
                expected_count=1,
                expected_queue="cpu-queue",
                expected_worker_names=["w1"],
                timeout_sec=60,
            )

    def test_assert_work_unit_jobsets(self) -> None:
        mock_stub = mock.MagicMock()
        raw_wu = SimpleNamespace(name="experiments/10/workUnits/1")
        mock_stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[raw_wu])
        expected_jobs = [{"name": "w1", "replicas": 1}]
        job_results = [{"name": "w1", "replicas": 1, "primary_container": "w1"}]
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_jobsets",
            return_value=job_results,
        ) as mock_val:
            res = common.assert_work_unit_jobsets(mock_stub, "10", expected_jobs)
            self.assertEqual(res, job_results)
            mock_val.assert_called_once_with(work_unit=raw_wu, expected_jobs=expected_jobs)

        mock_stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[])
        with self.assertRaises(AssertionError):
            common.assert_work_unit_jobsets(mock_stub, "10", expected_jobs)
        self.assertEqual(common.assert_work_unit_jobsets(mock_stub, "10", None), [])

    def test_verify_standard_workload_stub_boundary_and_num_work_units_isolation(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/10", title="Test Workload"
        )
        mock_stub.ListWorkUnits.return_value = SimpleNamespace(
            work_units=[_make_fake_wu("10", 1, ["w1", "w2"])]
        )
        extra_step = mock.MagicMock()
        with (
            mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "3"}, clear=True),
            mock.patch.object(
                common, "get_flag", side_effect=lambda _, k, d=None: "10" if k == "xid" else d
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout,
        ):
            common.verify_standard_workload(
                ["script.py"],
                banner_title="Banner",
                summary_title="Summary",
                expected_title_pattern="Test",
                expected_jobs=[{"name": "w1", "replicas": 1}, {"name": "w2", "replicas": 1}],
                extra_step=extra_step,
            )
            extra_step.assert_called_once_with(mock_stub, "10")
            mock_channel.close.assert_called_once()
            output = mock_stdout.getvalue()
            self.assertIn("WorkUnits               : 1", output)
            self.assertIn("Workers                 : ['w1']", output)
            self.assertIn("Job 'w1': Replicas=1", output)
            self.assertIn("Job 'w2': Replicas=1", output)
            self.assertIn("Test Workload", output)

    def test_verify_standard_workload_worker_names_precedence_and_extra_step_none(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/16", title="Demo Workload"
        )
        mock_stub.ListWorkUnits.return_value = SimpleNamespace(
            work_units=[
                _make_fake_wu("16", 1, ["demo-worker-1"]),
                _make_fake_wu("16", 2, ["demo-worker-2"]),
            ]
        )
        with (
            mock.patch.object(
                common, "get_flag", side_effect=lambda _, k, d=None: "16" if k == "xid" else d
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ),
        ):
            common.verify_standard_workload(
                ["script.py"],
                banner_title="Banner",
                summary_title="Summary",
                expected_title_pattern="Demo Workload",
                expected_jobs=[{"name": "demo-worker-1", "replicas": 1}],
                expected_worker_names=["demo-worker-1", "demo-worker-2"],
                extra_step=None,
            )
            mock_channel.close.assert_called_once()

    def test_verify_standard_workload_exit_codes(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        for exc, expected_code in (
            (AssertionError("title mismatch"), 1),
            (RuntimeError("gRPC transport failure"), 2),
        ):
            with self.subTest(exc=type(exc).__name__, expected_code=expected_code):
                mock_stub.GetExperiment.side_effect = exc
                with (
                    mock.patch.object(
                        common,
                        "get_flag",
                        side_effect=lambda _, k, d=None: "10" if k == "xid" else d,
                    ),
                    mock.patch(
                        "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                        return_value=(mock_stub, mock_channel),
                    ),
                    mock.patch("sys.stderr", new_callable=io.StringIO),
                    self.assertRaises(SystemExit) as ctx,
                ):
                    common.verify_standard_workload(
                        ["script.py"],
                        banner_title="Banner",
                        summary_title="Summary",
                        expected_title_pattern="Test",
                        expected_worker_names=["w1"],
                    )
                self.assertEqual(ctx.exception.code, expected_code)

    def test_assert_clickhouse_metrics(self) -> None:
        expected_res = {"total_rows": 50, "metrics": []}
        with (
            mock.patch.object(
                common,
                "get_flag",
                side_effect=lambda _, k, d=None: True if k == "require_clickhouse_metrics" else d,
            ),
            mock.patch.object(
                common, "resolve_clickhouse_endpoint", return_value="http://127.0.0.1:8123"
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.validate_clickhouse_metrics",
                return_value=expected_res,
            ) as mock_val,
        ):
            res = common.assert_clickhouse_metrics(
                "23", expected_metric_names=["train/loss"], expected_min_rows=50, timeout_sec=120
            )
            self.assertEqual(res, expected_res)
            mock_val.assert_called_once_with(
                endpoint="http://127.0.0.1:8123",
                xid="23",
                expected_metric_names=["train/loss"],
                expected_min_rows=50,
                timeout_sec=120,
                strict_required=True,
            )

    def test_assert_dashboard_chart_data(self) -> None:
        expected_res = {"dashboard_id": "exp_23_overview", "chart_id": "c1", "total_points": 5}
        cases: list[tuple[str, dict[str, Any], dict[str, str], bool]] = [
            ("unset_env_and_flags", {}, {}, False),
            (
                "xmanager_ui_url_alone_is_not_strict",
                {},
                {"XMANAGER_UI_URL": "https://xmc.example.com"},
                False,
            ),
            ("require_clickhouse_metrics_flag", {"require_clickhouse_metrics": True}, {}, True),
            ("dashboard_endpoint_flag", {"dashboard_endpoint": "localhost:9090"}, {}, True),
            (
                "xmc_dashboard_endpoint_env",
                {},
                {"XMC_DASHBOARD_ENDPOINT": "https://dash.example.com"},
                True,
            ),
            (
                "dashboard_endpoint_env",
                {},
                {"DASHBOARD_ENDPOINT": "https://dash.example.com"},
                True,
            ),
        ]
        for name, flag_overrides, env_vars, expected_strict in cases:
            with self.subTest(case=name):
                with (
                    mock.patch.object(
                        common,
                        "get_flag",
                        side_effect=lambda _, k, d=None, fo=flag_overrides: fo.get(k, d),
                    ),
                    mock.patch.dict("os.environ", env_vars, clear=True),
                    mock.patch(
                        "tools.nightly_tests.xmc_entities_validator.validate_dashboard_chart_data",
                        return_value=expected_res,
                    ) as mock_val,
                ):
                    res = common.assert_dashboard_chart_data(
                        "exp_23_overview", min_hydrated_points=2, expected_chart_id="c1"
                    )
                    self.assertEqual(res, expected_res)
                    mock_val.assert_called_once_with(
                        base_endpoint=mock.ANY,
                        dashboard_id="exp_23_overview",
                        expected_chart_id="c1",
                        min_hydrated_points=2,
                        timeout_sec=20,
                        strict_required=expected_strict,
                    )


if __name__ == "__main__":
    unittest.main()
