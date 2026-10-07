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

"""Unit tests for tools.nightly_tests.workload_verification module."""

from __future__ import annotations

import io
import unittest
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any
from unittest import mock

from tools.nightly_tests import (
    endpoint_resolution,
    verifier_cli,
    workload_verification,
    xmc_entities_validator,
)


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


class WorkloadVerificationTest(unittest.TestCase):
    """Test cases for workload_verification.py step runners and orchestrators."""

    def test_assert_experiment(self) -> None:
        mock_stub = mock.MagicMock()
        mock_exp = SimpleNamespace(name="experiments/10", title="Test Exp")
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_experiment",
            return_value=mock_exp,
        ) as mock_val:
            res = workload_verification.assert_experiment(mock_stub, "10", "Test")
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
            res = workload_verification.assert_work_units(
                mock_stub, "10", 1, "cpu-queue", ["w1"], 60
            )
            self.assertEqual(res, wu_list)
            mock_val.assert_called_once_with(
                stub=mock_stub,
                xid="10",
                expected_count=1,
                expected_queue="cpu-queue",
                expected_worker_names=["w1"],
                timeout_sec=60,
                wait_for_completion=True,
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
            res = workload_verification.assert_work_unit_jobsets(mock_stub, "10", expected_jobs)
            self.assertEqual(res, job_results)
            mock_val.assert_called_once_with(work_unit=raw_wu, expected_jobs=expected_jobs)

        mock_stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[])
        with self.assertRaises(AssertionError):
            workload_verification.assert_work_unit_jobsets(mock_stub, "10", expected_jobs)
        self.assertEqual(workload_verification.assert_work_unit_jobsets(mock_stub, "10", None), [])

    def test_assert_work_unit_artifacts(self) -> None:
        mock_stub = mock.MagicMock()
        art_results = [
            {
                "name": "artifacts/1",
                "type": "chart",
                "title": "Plot",
                "metric_names": ["loss"],
                "url": "",
            }
        ]
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_artifacts",
            return_value=art_results,
        ) as mock_val:
            res = workload_verification.assert_work_unit_artifacts(
                mock_stub, "10", "1", ["chart"], ["loss"], timeout_sec=120
            )
            self.assertEqual(res, art_results)
            mock_val.assert_called_once_with(
                stub=mock_stub,
                xid="10",
                wid="1",
                expected_artifact_types=["chart"],
                expected_metric_names=["loss"],
                timeout_sec=60,
            )

    def test_verify_standard_workload_stub_boundary_and_num_work_units_isolation(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/10", title="Test Workload"
        )
        mock_stub.ListWorkUnits.return_value = SimpleNamespace(
            work_units=[_make_fake_wu("10", 1, ["w1", "w2"])]
        )
        chart_obj = SimpleNamespace(
            title="Loss Plot", metric_names=["loss"], status=2, error_message=""
        )
        mock_stub.ListArtifacts.return_value = SimpleNamespace(
            artifacts=[
                SimpleNamespace(
                    name="artifacts/1",
                    parent="experiments/10/workUnits/1",
                    payload=SimpleNamespace(chart=chart_obj, WhichOneof=lambda f: "chart"),
                )
            ]
        )
        extra_step = mock.MagicMock()
        flag_values = {"xid": "10", "endpoint": "custom-ess:9090"}
        with (
            mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "3"}, clear=True),
            mock.patch.object(
                verifier_cli,
                "get_flag",
                side_effect=lambda _, k, d=None: flag_values.get(k, d),
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ) as mock_create_stub,
            mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout,
        ):
            workload_verification.verify_standard_workload(
                ["script.py"],
                banner_title="Banner",
                summary_title="Summary",
                expected_title_pattern="Test",
                expected_jobs=[{"name": "w1", "replicas": 1}, {"name": "w2", "replicas": 1}],
                expected_artifact_types=["chart"],
                expected_metric_names=["loss"],
                extra_step=extra_step,
            )
            mock_create_stub.assert_called_once_with("custom-ess:9090")
            extra_step.assert_called_once_with(mock_stub, "10")
            mock_channel.close.assert_called_once()
            output = mock_stdout.getvalue()
            self.assertIn("WorkUnits               : 1", output)
            self.assertIn("Workers                 : ['w1']", output)
            self.assertIn("Job 'w1': Replicas=1", output)
            self.assertIn("Job 'w2': Replicas=1", output)
            self.assertIn("Artifact: Name='artifacts/1'", output)
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
                verifier_cli, "get_flag", side_effect=lambda _, k, d=None: "16" if k == "xid" else d
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ),
        ):
            workload_verification.verify_standard_workload(
                ["script.py"],
                banner_title="Banner",
                summary_title="Summary",
                expected_title_pattern="Demo Workload",
                expected_jobs=[{"name": "demo-worker-1", "replicas": 1}],
                expected_worker_names=["demo-worker-1", "demo-worker-2"],
                extra_step=None,
                wait_for_completion=False,
            )
            mock_stub.BatchStopWorkUnits.assert_called_once()
            mock_channel.close.assert_called_once()

    def test_verify_standard_workload_stops_work_units_on_assertion_failure(self) -> None:
        mock_stub, mock_channel = mock.MagicMock(), mock.MagicMock()
        mock_stub.GetExperiment.side_effect = AssertionError("title mismatch")
        with (
            mock.patch.object(
                verifier_cli, "get_flag", side_effect=lambda _, k, d=None: "16" if k == "xid" else d
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ),
            mock.patch("sys.stderr", new_callable=io.StringIO),
            self.assertRaises(SystemExit) as ctx,
        ):
            workload_verification.verify_standard_workload(
                ["script.py"], "Banner", "Summary", "Title", wait_for_completion=False
            )
        self.assertEqual(ctx.exception.code, 1)
        mock_stub.BatchStopWorkUnits.assert_called_once()

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
                        verifier_cli,
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
                    workload_verification.verify_standard_workload(
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
                verifier_cli,
                "get_flag",
                side_effect=lambda _, k, d=None: True if k == "require_clickhouse_metrics" else d,
            ),
            mock.patch.object(
                endpoint_resolution,
                "resolve_clickhouse_endpoint",
                return_value="http://127.0.0.1:8123",
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.validate_clickhouse_metrics",
                return_value=expected_res,
            ) as mock_val,
        ):
            res = workload_verification.assert_clickhouse_metrics(
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
                        verifier_cli,
                        "get_flag",
                        side_effect=lambda _, k, d=None, fo=flag_overrides: fo.get(k, d),
                    ),
                    mock.patch.dict("os.environ", env_vars, clear=True),
                    mock.patch(
                        "tools.nightly_tests.xmc_entities_validator.validate_dashboard_chart_data",
                        return_value=expected_res,
                    ) as mock_val,
                ):
                    res = workload_verification.assert_dashboard_chart_data(
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

    def test_assert_dashboard(self) -> None:
        dash_payload = {
            "id": "dash_1",
            "title": "VCC Dashboard",
            "chart_ids": ["c1", "c2", "c3"],
            "charts": [{"name": "c1", "title": "Loss"}, {"id": "c2", "title": "Acc"}],
        }
        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.validate_dashboard",
            return_value=[dash_payload],
        ) as mock_val:
            res = workload_verification.assert_dashboard(
                "http://localhost:8080", "dash_1", "VCC", min_charts=3
            )
            self.assertEqual(res, dash_payload)
            mock_val.assert_called_once_with(
                endpoint="http://localhost:8080",
                expected_dashboards=[
                    {"dashboard_id": "dash_1", "expected_title": "VCC", "min_charts": 3}
                ],
                timeout_sec=120,
            )
        with (
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.validate_dashboard", return_value=[]
            ),
            self.assertRaisesRegex(AssertionError, "was not returned by endpoint"),
        ):
            workload_verification.assert_dashboard("http://localhost:8080", "dash_1", "VCC")

    def test_verify_dashboard_workload(self) -> None:
        dash_payload = {
            "id": "example_vcc_comparison_42",
            "title": "VCC Comparative",
            "chart_ids": ["c1", "c2", "c3"],
            "charts": [{"name": "c1", "title": "Loss"}],
        }
        flags_map = {"xid": "42", "dashboard_endpoint": "localhost:9090"}
        with (
            mock.patch.object(
                verifier_cli, "get_flag", side_effect=lambda _, k, d=None: flags_map.get(k, d)
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.validate_dashboard",
                return_value=[dash_payload],
            ) as mock_val,
            mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout,
        ):
            workload_verification.verify_dashboard_workload(
                ["script.py"],
                banner_title="Dashboard Banner",
                summary_title="DASHBOARD PASSED",
                default_dashboard_id="example_vcc_comparison",
                expected_title_pattern="VCC",
                expected_min_charts=3,
            )
            mock_val.assert_called_once()
            out = mock_stdout.getvalue()
            self.assertIn("example_vcc_comparison_42", out)
            self.assertIn("DASHBOARD PASSED", out)

        # Non-numeric slug dashboard_id and error exit via handle_verification_errors
        slug_flags = {"dashboard_id": "custom_slug", "dashboard_endpoint": "localhost:9090"}
        with (
            mock.patch.object(
                verifier_cli, "get_flag", side_effect=lambda _, k, d=None: slug_flags.get(k, d)
            ),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.validate_dashboard",
                return_value=[],
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout,
            mock.patch("sys.stderr", new_callable=io.StringIO),
            self.assertRaises(SystemExit) as ctx,
        ):
            workload_verification.verify_dashboard_workload(
                ["script.py"],
                banner_title="Dashboard Banner",
                summary_title="DASHBOARD PASSED",
                default_dashboard_id="example_vcc_comparison",
                expected_title_pattern="VCC",
            )
        self.assertEqual(ctx.exception.code, 1)
        self.assertIn("custom_slug", mock_stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
