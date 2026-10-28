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

"""Unit tests for workload validator module."""

from __future__ import annotations

import io
import json
import unittest
import urllib.error
import urllib.request
from types import SimpleNamespace
from typing import Any
from unittest import mock

from tools.nightly_tests.verification import xmc_entities_validator


class XmcEntitiesValidatorTest(unittest.TestCase):
    """Test suite for xmc_entities_validator library."""

    def test_state_enum_to_name(self) -> None:
        self.assertEqual(
            xmc_entities_validator.state_enum_to_name(xmc_entities_validator.STATE_COMPLETED),
            "COMPLETED",
        )
        self.assertEqual(
            xmc_entities_validator.state_enum_to_name(xmc_entities_validator.STATE_RUNNING),
            "RUNNING",
        )
        self.assertEqual(
            xmc_entities_validator.state_enum_to_name(xmc_entities_validator.STATE_FAILED),
            "FAILED",
        )
        self.assertEqual(xmc_entities_validator.state_enum_to_name(9999), "UNKNOWN(9999)")

    def test_extract_worker_name(self) -> None:
        # From JobSet job
        job = SimpleNamespace(name="trainer-worker-2")
        jobset = SimpleNamespace(jobs=[job])
        executable = SimpleNamespace(
            jobset=jobset,
            HasField=lambda field: field == "jobset",
        )
        wu = SimpleNamespace(executable=executable)
        self.assertEqual(xmc_entities_validator.extract_worker_name(wu), "trainer-worker-2")

        # Unknown fallback when executable is missing or empty
        wu_empty = SimpleNamespace()
        self.assertEqual(xmc_entities_validator.extract_worker_name(wu_empty), "unknown")

    def test_extract_queue_name(self) -> None:
        # From scheduling constraints
        constraints = SimpleNamespace(queue_name="gpu-queue")
        executable = SimpleNamespace(
            scheduling_constraints=constraints,
            HasField=lambda field: field == "scheduling_constraints",
        )
        wu = SimpleNamespace(executable=executable)
        self.assertEqual(xmc_entities_validator.extract_queue_name(wu), "gpu-queue")

        # Unknown fallback when scheduling constraints are missing
        wu_empty = SimpleNamespace()
        self.assertEqual(xmc_entities_validator.extract_queue_name(wu_empty), "unknown")

    def test_extract_work_unit_state(self) -> None:
        stub = mock.MagicMock()
        status = SimpleNamespace(state=xmc_entities_validator.STATE_COMPLETED)
        execution = SimpleNamespace(
            status=status,
            HasField=lambda field: field == "status",
        )
        wu = SimpleNamespace(
            name="experiments/10/workUnits/1",
            current_work_unit_execution=execution,
            HasField=lambda field: field == "current_work_unit_execution",
        )
        state = xmc_entities_validator.extract_work_unit_state(stub, wu)
        self.assertEqual(state, xmc_entities_validator.STATE_COMPLETED)
        stub.GetCurrentWorkUnitExecution.assert_not_called()

        # Unspecified state (0) should not trigger fallback query
        status_unspecified = SimpleNamespace(state=xmc_entities_validator.STATE_UNSPECIFIED)
        execution_unspecified = SimpleNamespace(
            status=status_unspecified,
            HasField=lambda field: field == "status",
        )
        wu_unspecified = SimpleNamespace(
            name="experiments/10/workUnits/3",
            current_work_unit_execution=execution_unspecified,
            HasField=lambda field: field == "current_work_unit_execution",
        )
        state_unspec = xmc_entities_validator.extract_work_unit_state(stub, wu_unspecified)
        self.assertEqual(state_unspec, xmc_entities_validator.STATE_UNSPECIFIED)
        stub.GetCurrentWorkUnitExecution.assert_not_called()

        # Fallback to stub query
        wu_no_exec = SimpleNamespace(
            name="experiments/10/workUnits/2",
            HasField=lambda field: False,
        )
        mock_exec = SimpleNamespace(
            status=SimpleNamespace(state=xmc_entities_validator.STATE_RUNNING),
        )
        stub.GetCurrentWorkUnitExecution.return_value = mock_exec
        state2 = xmc_entities_validator.extract_work_unit_state(stub, wu_no_exec)
        self.assertEqual(state2, xmc_entities_validator.STATE_RUNNING)

    def test_extract_work_unit_state_rpc_failure_logs_warning(self) -> None:
        stub = mock.MagicMock()
        stub.GetCurrentWorkUnitExecution.side_effect = RuntimeError("gRPC connection reset")
        wu = SimpleNamespace(name="experiments/10/workUnits/3")

        with self.assertLogs(level="WARNING") as log_cm:
            state = xmc_entities_validator.extract_work_unit_state(stub, wu)
            self.assertEqual(state, xmc_entities_validator.STATE_UNSPECIFIED)
            self.assertTrue(
                any(
                    "Failed to fetch state for experiments/10/workUnits/3" in msg
                    for msg in log_cm.output
                )
            )

    def test_validate_experiment_success(self) -> None:
        stub = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/42",
            title="Demo Workload (3 units, 60s execution)",
            creator="test@google.com",
        )
        exp = xmc_entities_validator.validate_experiment(stub, "42", "Demo Workload")
        self.assertEqual(exp.name, "experiments/42")

        # Test with None expected_title_pattern
        exp_no_pattern = xmc_entities_validator.validate_experiment(stub, "42")
        self.assertEqual(exp_no_pattern.name, "experiments/42")

    def test_validate_experiment_title_mismatch(self) -> None:
        stub = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/42",
            title="Production Training Run",
        )
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_experiment(stub, "42", "Demo Workload")
        self.assertIn("Experiment title mismatch", str(ctx.exception))

    def _create_mock_work_unit(
        self, xid: str, index: int, queue: str, state: int
    ) -> SimpleNamespace:
        worker_name = f"demo-worker-{index}"
        job = SimpleNamespace(name=worker_name)
        jobset = SimpleNamespace(jobs=[job])
        constraints = SimpleNamespace(queue_name=queue)
        executable = SimpleNamespace(
            jobset=jobset,
            scheduling_constraints=constraints,
            HasField=lambda f: f in ("jobset", "scheduling_constraints"),
        )
        status = SimpleNamespace(state=state)
        execution = SimpleNamespace(
            status=status,
            HasField=lambda f: f == "status",
        )
        return SimpleNamespace(
            name=f"experiments/{xid}/workUnits/{index}",
            executable=executable,
            tags=[queue, worker_name],
            current_work_unit_execution=execution,
            HasField=lambda f: f == "current_work_unit_execution",
        )

    def test_validate_work_units_success_multi_unit(self) -> None:
        stub = mock.MagicMock()
        work_units = [
            self._create_mock_work_unit(
                "16", i, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
            )
            for i in range(1, 4)
        ]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=work_units)

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=3,
            expected_queue="cpu-queue",
            expected_worker_names=["demo-worker-1", "demo-worker-2", "demo-worker-3"],
            timeout_sec=10,
            wait_for_completion=True,
        )
        self.assertEqual(len(results), 3)
        self.assertEqual(
            [r["worker_name"] for r in results],
            ["demo-worker-1", "demo-worker-2", "demo-worker-3"],
        )
        self.assertEqual(results[0]["state"], "COMPLETED")

    def test_validate_work_units_omitted_worker_names(self) -> None:
        stub = mock.MagicMock()
        work_units = [
            self._create_mock_work_unit(
                "16", i, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
            )
            for i in range(1, 4)
        ]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=work_units)

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=3,
            expected_queue="cpu-queue",
            timeout_sec=10,
            wait_for_completion=True,
        )
        self.assertEqual(len(results), 3)
        self.assertEqual(results[0]["state"], "COMPLETED")

    def test_validate_work_units_single_unit(self) -> None:
        stub = mock.MagicMock()
        job = SimpleNamespace(name="demo-worker")
        jobset = SimpleNamespace(jobs=[job])
        constraints = SimpleNamespace(queue_name="cpu-queue")
        executable = SimpleNamespace(
            jobset=jobset,
            scheduling_constraints=constraints,
            HasField=lambda f: f in ("jobset", "scheduling_constraints"),
        )
        status = SimpleNamespace(state=xmc_entities_validator.STATE_COMPLETED)
        execution = SimpleNamespace(
            status=status,
            HasField=lambda f: f == "status",
        )
        wu = SimpleNamespace(
            name="experiments/16/workUnits/1",
            executable=executable,
            tags=["cpu-queue", "demo-worker"],
            current_work_unit_execution=execution,
            HasField=lambda f: f == "current_work_unit_execution",
        )
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=1,
            expected_queue="cpu-queue",
            timeout_sec=10,
            wait_for_completion=True,
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["worker_name"], "demo-worker")

    def test_validate_work_units_queue_mismatch(self) -> None:
        stub = mock.MagicMock()
        wu = self._create_mock_work_unit(
            "16", 1, "gpu-queue", xmc_entities_validator.STATE_COMPLETED
        )
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_units(
                stub=stub,
                xid="16",
                expected_count=1,
                expected_queue="cpu-queue",
                timeout_sec=300,
                wait_for_completion=False,
            )
        self.assertIn("targeted queue 'gpu-queue', expected 'cpu-queue'", str(ctx.exception))
        self.assertEqual(stub.ListWorkUnits.call_count, 1)

    def test_terminal_failure_error_hierarchy(self) -> None:
        self.assertTrue(issubclass(xmc_entities_validator.TerminalFailureError, AssertionError))

    def test_validate_work_units_failure_state(self) -> None:
        stub = mock.MagicMock()
        job = SimpleNamespace(name="demo-worker")
        jobset = SimpleNamespace(jobs=[job])
        constraints = SimpleNamespace(queue_name="cpu-queue")
        executable = SimpleNamespace(
            jobset=jobset,
            scheduling_constraints=constraints,
            HasField=lambda f: f in ("jobset", "scheduling_constraints"),
        )
        status = SimpleNamespace(state=xmc_entities_validator.STATE_FAILED)
        execution = SimpleNamespace(
            status=status,
            HasField=lambda f: f == "status",
        )
        wu = SimpleNamespace(
            name="experiments/16/workUnits/1",
            executable=executable,
            tags=["cpu-queue", "demo-worker"],
            current_work_unit_execution=execution,
            HasField=lambda f: f == "current_work_unit_execution",
        )
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        with self.assertRaises(xmc_entities_validator.TerminalFailureError) as ctx:
            xmc_entities_validator.validate_work_units(
                stub=stub,
                xid="16",
                expected_count=1,
                expected_queue="cpu-queue",
                timeout_sec=300,
                wait_for_completion=True,
            )
        self.assertIn("failed execution", str(ctx.exception))
        self.assertEqual(stub.ListWorkUnits.call_count, 1)

    def test_validate_work_units_transient_error_retry(self) -> None:
        stub = mock.MagicMock()
        wu = self._create_mock_work_unit(
            "16", 1, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
        )
        stub.ListWorkUnits.side_effect = [
            RuntimeError("Transient network failure"),
            SimpleNamespace(work_units=[wu]),
        ]

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=1,
            expected_queue="cpu-queue",
            expected_worker_names=["demo-worker-1"],
            timeout_sec=10,
            poll_interval_sec=0,
            wait_for_completion=True,
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(stub.ListWorkUnits.call_count, 2)

    def test_validate_work_units_transient_error_timeout(self) -> None:
        stub = mock.MagicMock()
        stub.ListWorkUnits.side_effect = RuntimeError("Persistent network failure")

        with self.assertRaises(RuntimeError) as ctx:
            xmc_entities_validator.validate_work_units(
                stub=stub,
                xid="16",
                expected_count=1,
                expected_queue="cpu-queue",
                timeout_sec=0,
                poll_interval_sec=0,
                wait_for_completion=True,
            )
        self.assertIn("Persistent network failure", str(ctx.exception))

    def test_validate_work_units_count_exceeded_fail_fast(self) -> None:
        stub = mock.MagicMock()
        work_units = [
            self._create_mock_work_unit(
                "16", i, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
            )
            for i in range(1, 4)
        ]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=work_units)

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_units(
                stub=stub,
                xid="16",
                expected_count=2,
                expected_queue="cpu-queue",
                timeout_sec=300,
                wait_for_completion=True,
            )
        self.assertIn("WorkUnit count exceeded: expected 2, found 3", str(ctx.exception))
        self.assertEqual(stub.ListWorkUnits.call_count, 1)

    def test_resolve_ui_base_url(self) -> None:
        with mock.patch.dict(
            "os.environ", {"GCP_DOMAIN_NAME": "xmc.example.com", "XMANAGER_UI_URL": ""}, clear=True
        ):
            self.assertEqual(
                xmc_entities_validator.resolve_ui_base_url(), "https://xmc.example.com"
            )

        with mock.patch.dict(
            "os.environ", {"GCP_DOMAIN_NAME": "localhost:8080", "XMANAGER_UI_URL": ""}, clear=True
        ):
            self.assertEqual(xmc_entities_validator.resolve_ui_base_url(), "http://localhost:8080")

        with mock.patch.dict(
            "os.environ", {"GCP_DOMAIN_NAME": "127.0.0.1:3000", "XMANAGER_UI_URL": ""}, clear=True
        ):
            self.assertEqual(xmc_entities_validator.resolve_ui_base_url(), "http://127.0.0.1:3000")

        with mock.patch.dict(
            "os.environ",
            {"GCP_DOMAIN_NAME": "http://custom-ui.local", "XMANAGER_UI_URL": ""},
            clear=True,
        ):
            self.assertEqual(xmc_entities_validator.resolve_ui_base_url(), "http://custom-ui.local")

        with mock.patch.dict(
            "os.environ", {"XMANAGER_UI_URL": "https://direct-ui.internal/"}, clear=True
        ):
            self.assertEqual(
                xmc_entities_validator.resolve_ui_base_url(), "https://direct-ui.internal"
            )

    def test_make_req_duck_typing(self) -> None:
        req = xmc_entities_validator._make_req("NonExistentRequestClass", foo="bar", answer=42)
        self.assertIsInstance(req, SimpleNamespace)
        self.assertEqual(req.foo, "bar")
        self.assertEqual(req.answer, 42)

    def test_evaluate_work_units_count_mismatch(self) -> None:
        stub = mock.MagicMock()
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator._evaluate_work_units(
                stub=stub,
                work_units=[],
                expected_count=2,
                expected_queue="cpu-queue",
                expected_worker_names=["demo-worker-1", "demo-worker-2"],
            )
        self.assertIn("WorkUnit count mismatch", str(ctx.exception))

    def test_evaluate_work_units_missing_workers(self) -> None:
        stub = mock.MagicMock()
        wu = self._create_mock_work_unit(
            "16", 1, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
        )
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator._evaluate_work_units(
                stub=stub,
                work_units=[wu],
                expected_count=1,
                expected_queue="cpu-queue",
                expected_worker_names=["expected-other-worker"],
            )
        self.assertIn("Expected worker names", str(ctx.exception))

    def test_create_authenticated_stub_success(self) -> None:
        mock_ess_api = mock.MagicMock()
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_ess_api._create_experiment_state_server_stub.return_value = (mock_stub, mock_channel)

        with mock.patch.object(xmc_entities_validator, "ess_api", mock_ess_api):
            stub, channel = xmc_entities_validator.create_authenticated_stub("endpoint:443")
            mock_ess_api._create_experiment_state_server_stub.assert_called_once_with(
                "endpoint:443"
            )
            self.assertEqual(stub, mock_stub)
            self.assertEqual(channel, mock_channel)

    def test_create_authenticated_stub_missing_ess_api(self) -> None:
        with mock.patch.object(xmc_entities_validator, "ess_api", None):
            with self.assertRaises(RuntimeError) as ctx:
                xmc_entities_validator.create_authenticated_stub("endpoint:443")
            self.assertIn("xmanager_cloud is required", str(ctx.exception))

    def test_validate_work_unit_priority_success(self) -> None:
        cases: list[tuple[int, int | str]] = [
            (3, "HIGH"),
            (3, "high"),
            (3, 3),
            (2, "NORMAL"),
            (2, "normal"),
            (2, 2),
            (1, "LOW"),
            (1, "low"),
            (1, 1),
            (0, "PRIORITY_UNSPECIFIED"),
            (0, 0),
        ]
        for priority_val, expected in cases:
            wu = SimpleNamespace(name="experiments/10/workUnits/1", priority=priority_val)
            xmc_entities_validator.validate_work_unit_priority(wu, expected)

    def test_validate_work_unit_priority_with_proto_enum(self) -> None:
        mock_priority_pb2 = SimpleNamespace(
            Priority=SimpleNamespace(
                HIGH=3,
                NORMAL=2,
                LOW=1,
                PRIORITY_UNSPECIFIED=0,
            )
        )
        with mock.patch.object(xmc_entities_validator, "priority_pb2", mock_priority_pb2):
            wu_high = SimpleNamespace(name="experiments/10/workUnits/1", priority=3)
            xmc_entities_validator.validate_work_unit_priority(wu_high, "HIGH")
            wu_norm = SimpleNamespace(name="experiments/10/workUnits/2", priority=2)
            xmc_entities_validator.validate_work_unit_priority(wu_norm, "NORMAL")
            wu_low = SimpleNamespace(name="experiments/10/workUnits/3", priority=1)
            xmc_entities_validator.validate_work_unit_priority(wu_low, "LOW")
            wu_unspec = SimpleNamespace(name="experiments/10/workUnits/4", priority=0)
            xmc_entities_validator.validate_work_unit_priority(wu_unspec, "PRIORITY_UNSPECIFIED")
            with self.assertRaises(AssertionError):
                xmc_entities_validator.validate_work_unit_priority(wu_high, "LOW")

    def test_validate_work_unit_priority_mismatch_and_errors(self) -> None:
        wu_high = SimpleNamespace(name="experiments/10/workUnits/1", priority=3)
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_unit_priority(wu_high, "LOW")
        self.assertIn("priority mismatch: expected 1, found 3", str(ctx.exception))

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_unit_priority(wu_high, 1)
        self.assertIn("priority mismatch: expected 1, found 3", str(ctx.exception))

        wu_low = SimpleNamespace(name="experiments/10/workUnits/2", priority=1)
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_unit_priority(wu_low, "HIGH")
        self.assertIn("priority mismatch: expected 3, found 1", str(ctx.exception))

        wu_no_prio = SimpleNamespace(name="experiments/10/workUnits/3")
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_unit_priority(wu_no_prio, "HIGH")
        self.assertIn("missing priority field", str(ctx.exception))

    def test_validate_resource_requests(self) -> None:
        resources = SimpleNamespace(
            cpu_resources=SimpleNamespace(cpu_millicores=500, memory_bytes=536870912),
            gpu_resources=SimpleNamespace(count=1),
        )
        spec = SimpleNamespace(resources=resources)
        job = SimpleNamespace(spec=spec)

        xmc_entities_validator.validate_resource_requests(
            job,
            expected_cpu_milli=500,
            expected_memory_bytes=536870912,
            expected_gpus=1,
        )

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_resource_requests(job, expected_cpu_milli=1000)

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_resource_requests(job, expected_memory_bytes=1024)

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_resource_requests(job, expected_gpus=2)

    def test_validate_work_unit_metadata(self) -> None:
        wu = SimpleNamespace(
            name="experiments/10/workUnits/1",
            notes="Demo workload 1/3 (demo-worker-1) targeted at cpu-queue",
            tags=["60s-workload", "cpu-queue", "demo-worker-1"],
        )
        xmc_entities_validator.validate_work_unit_metadata(
            wu,
            expected_tags=["60s-workload", "cpu-queue"],
            expected_notes_substring="Demo workload",
        )

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_work_unit_metadata(
                wu, expected_notes_substring="Production workload"
            )

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_work_unit_metadata(wu, expected_tags=["missing-tag"])

    def test_validate_jobsets(self) -> None:
        job = SimpleNamespace(
            name="demo-worker-1",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="primary-worker"),
        )
        wu = SimpleNamespace(
            name="experiments/10/workUnits/1",
            executable=SimpleNamespace(
                jobset=SimpleNamespace(jobs=[job]),
                HasField=lambda f: f == "jobset",
            ),
        )
        summaries = xmc_entities_validator.validate_jobsets(
            wu,
            expected_jobs=[{"name": "demo-worker-1", "replicas": 1}],
        )
        self.assertEqual(len(summaries), 1)
        self.assertEqual(summaries[0]["name"], "demo-worker-1")

        with self.assertRaises(AssertionError):
            xmc_entities_validator.validate_jobsets(
                wu,
                expected_jobs=[{"name": "wrong-worker", "replicas": 1}],
            )

    def test_wait_for_experiment_state(self) -> None:
        stub = mock.MagicMock()
        wu = self._create_mock_work_unit(
            "16", 1, "cpu-queue", xmc_entities_validator.STATE_COMPLETED
        )
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
        results = xmc_entities_validator.wait_for_experiment_state(stub, "16", timeout_sec=5)
        self.assertEqual(len(results), 1)

    def test_validate_clickhouse_metrics_success(self) -> None:
        mock_response = {
            "data": [
                {"metric_name": "train/loss", "row_count": 200, "avg_val": 1.25},
                {"metric_name": "train/accuracy", "row_count": 200, "avg_val": 0.85},
            ]
        }
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_clickhouse_sql",
            return_value=mock_response,
        ):
            res = xmc_entities_validator.validate_clickhouse_metrics(
                endpoint="http://localhost:8123",
                xid="22",
                expected_metric_names={"train/loss", "train/accuracy"},
                expected_min_rows=100,
                timeout_sec=5,
            )
            self.assertEqual(res["total_rows"], 400)
            self.assertEqual(len(res["metrics"]), 2)

    def test_validate_clickhouse_metrics_missing_metric_raises(self) -> None:
        mock_response = {
            "data": [
                {"metric_name": "train/loss", "row_count": 200, "avg_val": 1.25},
            ]
        }
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_clickhouse_sql",
            return_value=mock_response,
        ):
            with self.assertRaises(AssertionError) as ctx:
                xmc_entities_validator.validate_clickhouse_metrics(
                    endpoint="http://localhost:8123",
                    xid="22",
                    expected_metric_names={"train/loss", "train/accuracy"},
                    timeout_sec=0.05,
                    poll_interval_sec=0.01,
                )
            self.assertIn("Expected metrics ['train/accuracy'] not found", str(ctx.exception))

    def test_validate_clickhouse_metrics_retries_on_incomplete_metrics_until_success(self) -> None:
        responses = [
            {"data": [{"metric_name": "train/loss", "row_count": 200, "avg_val": 1.25}]},
            {
                "data": [
                    {"metric_name": "train/loss", "row_count": 200, "avg_val": 1.25},
                    {"metric_name": "train/accuracy", "row_count": 200, "avg_val": 0.85},
                ]
            },
        ]
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_clickhouse_sql",
            side_effect=responses,
        ):
            res = xmc_entities_validator.validate_clickhouse_metrics(
                endpoint="http://localhost:8123",
                xid="22",
                expected_metric_names={"train/loss", "train/accuracy"},
                timeout_sec=1.0,
                poll_interval_sec=0.01,
            )
            self.assertEqual(res["total_rows"], 400)
            self.assertEqual(len(res["metrics"]), 2)

    def test_validate_clickhouse_metrics_non_strict_returns_skipped_on_timeout(self) -> None:
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_clickhouse_sql",
            return_value=None,
        ):
            res = xmc_entities_validator.validate_clickhouse_metrics(
                endpoint="http://localhost:8123",
                xid="22",
                expected_metric_names={"train/loss"},
                timeout_sec=0.05,
                poll_interval_sec=0.01,
                strict_required=False,
            )
            self.assertTrue(res.get("skipped"))
            self.assertEqual(res["total_rows"], 0)

    def test_validate_dashboard_chart_data_success(self) -> None:
        dash_response = {
            "chartIds": [
                "dashboards/exp_22_training_overview/charts/loss_chart",
                "dashboards/exp_22_training_overview/charts/throughput_chart",
            ]
        }
        chart_response = {
            "plotsData": [
                {
                    "scatterSeries": {
                        "points": [{"step": 1, "value": 0.5}, {"step": 2, "value": 0.4}]
                    }
                }
            ]
        }
        queried_urls: list[str] = []

        def mock_query(url: str, **kwargs: Any) -> Any:
            queried_urls.append(url)
            if url.endswith(":getData"):
                return chart_response
            return dash_response

        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_dashboard_http",
            side_effect=mock_query,
        ):
            res = xmc_entities_validator.validate_dashboard_chart_data(
                base_endpoint="http://localhost:8080",
                dashboard_id="exp_22_training_overview",
                timeout_sec=5,
            )
            self.assertEqual(res["dashboard_id"], "exp_22_training_overview")
            self.assertEqual(res["chart_id"], "loss_chart")
            self.assertEqual(res["total_points"], 4)
            self.assertEqual(
                queried_urls,
                [
                    "http://localhost:8080/v1/dashboards/exp_22_training_overview",
                    "http://localhost:8080/v1/dashboards/exp_22_training_overview/charts/loss_chart:getData",
                    "http://localhost:8080/v1/dashboards/exp_22_training_overview/charts/throughput_chart:getData",
                ],
            )
            queried_urls.clear()
            single_res = xmc_entities_validator.validate_dashboard_chart_data(
                base_endpoint="http://localhost:8080",
                dashboard_id="exp_22_training_overview",
                expected_chart_id="dashboards/exp_22_training_overview/charts/throughput_chart",
                timeout_sec=5,
            )
            self.assertEqual(single_res["chart_id"], "throughput_chart")
            self.assertEqual(single_res["total_points"], 2)
            self.assertEqual(
                queried_urls,
                [
                    "http://localhost:8080/v1/dashboards/exp_22_training_overview",
                    "http://localhost:8080/v1/dashboards/exp_22_training_overview/charts/throughput_chart:getData",
                ],
            )

    # =========================================================================
    # Phase 2: validate_artifacts Tests
    # =========================================================================

    def test_normalize_artifact_type(self) -> None:
        self.assertEqual(xmc_entities_validator._normalize_artifact_type("ChartArtifact"), "chart")
        self.assertEqual(xmc_entities_validator._normalize_artifact_type("UrlArtifact"), "url")
        self.assertEqual(
            xmc_entities_validator._normalize_artifact_type("ManagedXprofArtifact"), "managed_xprof"
        )
        self.assertEqual(xmc_entities_validator._normalize_artifact_type("chart"), "chart")
        self.assertEqual(
            xmc_entities_validator._normalize_artifact_type("ArtifactPayload.chart"), "chart"
        )

    def test_validate_artifacts_success_with_chart_and_url(self) -> None:
        stub = mock.MagicMock()
        chart_obj = SimpleNamespace(
            title="Loss and Accuracy Plots",
            metric_names=["val/loss", "train/accuracy"],
            chart=SimpleNamespace(name="charts/1"),
            status=2,  # SUCCESS
            error_message="",
        )
        url_obj = SimpleNamespace(
            url="https://dashboard.example.com/view",
            display_name="External Monitoring",
        )
        art_chart = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(
                chart=chart_obj, WhichOneof=lambda f: "chart" if f == "type" else None
            ),
        )
        art_url = SimpleNamespace(
            name="artifacts/2",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(
                url=url_obj, WhichOneof=lambda f: "url" if f == "type" else None
            ),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art_chart, art_url])

        results = xmc_entities_validator.validate_artifacts(
            stub=stub,
            xid="10",
            wid="1",
            expected_artifact_types=["ChartArtifact", "UrlArtifact"],
            expected_metric_names=["val/loss", "train/accuracy"],
            timeout_sec=10,
        )
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0]["type"], "chart")
        self.assertEqual(results[0]["title"], "Loss and Accuracy Plots")
        self.assertEqual(results[0]["metric_names"], ["val/loss", "train/accuracy"])
        self.assertEqual(results[1]["type"], "url")
        self.assertEqual(results[1]["title"], "External Monitoring")
        self.assertEqual(results[1]["url"], "https://dashboard.example.com/view")

    def test_validate_artifacts_waits_for_chart_provisioning(self) -> None:
        stub = mock.MagicMock()
        chart_provisioning = SimpleNamespace(
            title="Loss Plot",
            metric_names=["val/loss"],
            status=1,  # PROVISIONING
            error_message="",
        )
        chart_ready = SimpleNamespace(
            title="Loss Plot",
            metric_names=["val/loss"],
            status=2,  # SUCCESS
            error_message="",
        )
        art_provisioning = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(chart=chart_provisioning, WhichOneof=lambda f: "chart"),
        )
        art_ready = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(chart=chart_ready, WhichOneof=lambda f: "chart"),
        )
        stub.ListArtifacts.side_effect = [
            SimpleNamespace(artifacts=[art_provisioning]),
            SimpleNamespace(artifacts=[art_ready]),
        ]

        results = xmc_entities_validator.validate_artifacts(
            stub=stub,
            xid="10",
            wid="1",
            expected_artifact_types=["chart"],
            expected_metric_names=["val/loss"],
            timeout_sec=5,
            poll_interval_sec=0,
        )
        self.assertEqual(stub.ListArtifacts.call_count, 2)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["normalized_type"], "chart")
        self.assertEqual(results[0]["metric_names"], ["val/loss"])

    def test_validate_artifacts_experiment_level(self) -> None:
        stub = mock.MagicMock()
        url_obj = SimpleNamespace(url="https://experiment.example.com", display_name="Summary")
        art = SimpleNamespace(
            name="artifacts/100",
            parent="experiments/42",
            payload=SimpleNamespace(url=url_obj, WhichOneof=lambda f: "url"),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        results = xmc_entities_validator.validate_artifacts(
            stub=stub,
            xid="42",
            expected_artifact_types=["url"],
            timeout_sec=10,
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["parent"], "experiments/42")

    def test_validate_artifacts_terminal_failure_on_chart_failed_status(self) -> None:
        stub = mock.MagicMock()
        chart_obj = SimpleNamespace(
            title="Failed Chart",
            metric_names=["loss"],
            status=3,  # FAILED
            error_message="ClickHouse connection timed out",
        )
        art = SimpleNamespace(
            name="artifacts/5",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(chart=chart_obj, WhichOneof=lambda f: "chart"),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        with self.assertRaises(xmc_entities_validator.TerminalFailureError) as ctx:
            xmc_entities_validator.validate_artifacts(
                stub=stub,
                xid="10",
                wid="1",
                timeout_sec=5,
            )
        self.assertIn("failed provisioning: ClickHouse connection timed out", str(ctx.exception))

    def test_validate_artifacts_missing_expected_types_fails(self) -> None:
        stub = mock.MagicMock()
        url_obj = SimpleNamespace(url="https://example.com", display_name="")
        art = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10",
            payload=SimpleNamespace(url=url_obj, WhichOneof=lambda f: "url"),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_artifacts(
                stub=stub,
                xid="10",
                expected_artifact_types=["chart"],
                timeout_sec=0,
                poll_interval_sec=0,
            )
        self.assertIn("Expected artifact types ['chart'] not found", str(ctx.exception))

    def test_validate_artifacts_missing_expected_metrics_fails(self) -> None:
        stub = mock.MagicMock()
        chart_obj = SimpleNamespace(
            title="Chart",
            metric_names=["train/loss"],
            status=2,
            error_message="",
        )
        art = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(chart=chart_obj, WhichOneof=lambda f: "chart"),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_artifacts(
                stub=stub,
                xid="10",
                wid="1",
                expected_metric_names=["val/accuracy"],
                timeout_sec=0,
                poll_interval_sec=0,
            )
        self.assertIn("Expected metric names ['val/accuracy'] not found", str(ctx.exception))

    def test_validate_artifacts_transient_error_retry(self) -> None:
        stub = mock.MagicMock()
        url_obj = SimpleNamespace(url="https://example.com", display_name="")
        art = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/10",
            payload=SimpleNamespace(url=url_obj, WhichOneof=lambda f: "url"),
        )
        stub.ListArtifacts.side_effect = [
            RuntimeError("gRPC transient error"),
            SimpleNamespace(artifacts=[art]),
        ]

        results = xmc_entities_validator.validate_artifacts(
            stub=stub,
            xid="10",
            expected_artifact_types=["url"],
            timeout_sec=10,
            poll_interval_sec=0,
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(stub.ListArtifacts.call_count, 2)

    def test_validate_artifacts_empty_url_raises(self) -> None:
        stub = mock.MagicMock()
        url_obj = SimpleNamespace(url="   ", display_name="Broken Link")
        art = SimpleNamespace(
            name="artifacts/99",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(url=url_obj, WhichOneof=lambda f: "url"),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_artifacts(
                stub=stub,
                xid="10",
                wid="1",
                expected_artifact_types=["url"],
                timeout_sec=5,
                poll_interval_sec=0,
            )
        self.assertIn("Found URL artifact 'artifacts/99' with empty URL field", str(ctx.exception))

    # =========================================================================
    # Phase 2: validate_dashboard Tests
    # =========================================================================

    def test_validate_dashboard_success_with_charts(self) -> None:
        routes = {
            "/v1/dashboards/example_vcc": {
                "name": "dashboards/example_vcc",
                "title": "VCC Comparative Dashboard",
                "chart_ids": ["chart_1", "chart_2"],
            },
            "/v1/charts/chart_1": {"name": "charts/chart_1", "title": "Loss vs Steps"},
            "/v1/charts/chart_2": {"name": "charts/chart_2", "title": "Accuracy vs Steps"},
        }
        expected = {
            "dashboard_id": "example_vcc",
            "expected_title": "VCC Comparative",
            "expected_chart_ids": ["chart_1", "chart_2"],
        }

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            for path, payload in routes.items():
                if path in req.full_url:
                    return io.BytesIO(json.dumps(payload).encode("utf-8"))
            raise urllib.error.HTTPError(req.full_url, 404, "Not Found", {}, None)  # type: ignore

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            results = xmc_entities_validator.validate_dashboard(
                "localhost:8080", [expected], timeout_sec=5
            )
            self.assertEqual(len(results), 1)
            self.assertEqual(results[0]["dashboard_id"], "example_vcc")
            self.assertEqual(results[0]["title"], "VCC Comparative Dashboard")
            self.assertEqual(len(results[0]["charts"]), 2)

    def test_validate_dashboard_string_identifier(self) -> None:
        mock_dashboard_data = {
            "name": "dashboards/simple_dash",
            "title": "Simple Dashboard",
            "chart_ids": [],
        }

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            resp = io.BytesIO(json.dumps(mock_dashboard_data).encode("utf-8"))
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            results = xmc_entities_validator.validate_dashboard(
                dashboard_endpoint="http://dashboard-service:8080",
                expected_dashboards=["simple_dash"],
                timeout_sec=5,
            )
            self.assertEqual(len(results), 1)
            self.assertEqual(results[0]["dashboard_id"], "simple_dash")

    def test_validate_dashboard_title_mismatch_fails(self) -> None:
        mock_dashboard_data = {
            "name": "dashboards/dash_1",
            "title": "Production Overview",
            "chart_ids": [],
        }

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            resp = io.BytesIO(json.dumps(mock_dashboard_data).encode("utf-8"))
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with self.assertRaises(AssertionError) as ctx:
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=[
                        {"dashboard_id": "dash_1", "expected_title": "Experimental"}
                    ],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )
            self.assertIn(
                "title mismatch: expected 'Experimental', found 'Production Overview'",
                str(ctx.exception),
            )

    def test_validate_dashboard_missing_expected_chart_ids(self) -> None:
        mock_dashboard_data = {
            "name": "dashboards/dash_1",
            "title": "Dashboard",
            "chart_ids": ["chart_a"],
        }

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            resp = io.BytesIO(json.dumps(mock_dashboard_data).encode("utf-8"))
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with self.assertRaises(AssertionError) as ctx:
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=[
                        {
                            "dashboard_id": "dash_1",
                            "expected_chart_ids": ["chart_a", "chart_b"],
                        }
                    ],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )
            self.assertIn("missing expected chart IDs: ['chart_b']", str(ctx.exception))

    def test_validate_dashboard_non_200_status(self) -> None:
        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> None:
            raise urllib.error.HTTPError(
                req.full_url,
                500,
                "Internal Server Error",
                mock.MagicMock(),
                None,
            )

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with self.assertRaises(TimeoutError) as ctx:
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=["dash_1"],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )
            self.assertIn("verification failed after 0s", str(ctx.exception))

    def test_validate_dashboard_network_error_timeout(self) -> None:
        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> None:
            raise urllib.error.URLError("Connection refused")

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with self.assertRaises(TimeoutError) as ctx:
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=["dash_1"],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )
            self.assertIn("verification failed after 0s", str(ctx.exception))

    def test_validate_dashboard_retry_on_assertion_error_success(self) -> None:
        attempts = 0

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                # First attempt: incomplete title
                data = {"name": "dashboards/dash_1", "title": "Pending...", "chart_ids": []}
            else:
                # Second attempt: ready title
                data = {"name": "dashboards/dash_1", "title": "Ready Title", "chart_ids": []}
            resp = io.BytesIO(json.dumps(data).encode("utf-8"))
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            results = xmc_entities_validator.validate_dashboard(
                dashboard_endpoint="localhost:8080",
                expected_dashboards=[{"dashboard_id": "dash_1", "expected_title": "Ready Title"}],
                timeout_sec=5,
                poll_interval_sec=0,
            )
            self.assertEqual(len(results), 1)
            self.assertEqual(results[0]["title"], "Ready Title")
            self.assertEqual(attempts, 2)

    def test_validate_dashboard_retry_on_json_decode_error_success(self) -> None:
        attempts = 0

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                # First attempt: transient HTML 502/error text
                resp = io.BytesIO(b"<html>502 Bad Gateway</html>")
            else:
                # Second attempt: valid JSON response
                resp = io.BytesIO(
                    json.dumps(
                        {"name": "dashboards/dash_1", "title": "Ready", "chart_ids": []}
                    ).encode("utf-8")
                )
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            results = xmc_entities_validator.validate_dashboard(
                dashboard_endpoint="localhost:8080",
                expected_dashboards=["dash_1"],
                timeout_sec=5,
                poll_interval_sec=0,
            )
            self.assertEqual(len(results), 1)
            self.assertEqual(results[0]["title"], "Ready")
            self.assertEqual(attempts, 2)

    def test_validate_dashboard_json_decode_error_timeout(self) -> None:
        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            resp = io.BytesIO(b"Invalid Non-JSON response")
            resp.status = 200  # type: ignore
            return resp

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            with self.assertRaises(TimeoutError) as ctx:
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=["dash_1"],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )
            self.assertIn("verification failed after 0s", str(ctx.exception))

    def test_validate_work_unit_jobsets(self) -> None:
        job = SimpleNamespace(
            name="demo-worker-1",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="primary-worker"),
        )
        wu = SimpleNamespace(
            name="experiments/10/workUnits/1",
            executable=SimpleNamespace(
                jobset=SimpleNamespace(jobs=[job]),
                HasField=lambda f: f == "jobset",
            ),
        )
        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
        summaries = xmc_entities_validator.validate_work_unit_jobsets(
            stub=stub,
            xid="10",
            expected_jobs=[{"name": "demo-worker-1", "replicas": 1}],
        )
        self.assertEqual(len(summaries), 1)
        self.assertEqual(summaries[0]["name"], "demo-worker-1")

        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[])
        with self.assertRaisesRegex(AssertionError, "No WorkUnits found for experiment 10"):
            xmc_entities_validator.validate_work_unit_jobsets(
                stub=stub,
                xid="10",
                expected_jobs=[{"name": "demo-worker-1", "replicas": 1}],
            )

    def test_validate_dashboard_chart_data_timeout_includes_duration(self) -> None:
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_dashboard_http",
            return_value={"chartIds": []},
        ):
            with self.assertRaises(AssertionError) as ctx:
                xmc_entities_validator.validate_dashboard_chart_data(
                    base_endpoint="http://localhost:8080",
                    dashboard_id="exp_22_training_overview",
                    timeout_sec=0.05,  # type: ignore[arg-type]
                    poll_interval_sec=0.01,  # type: ignore[arg-type]
                    strict_required=True,
                )
            self.assertIn(
                "Dashboard GetChartData failed after 0.05s (exp_22_training_overview):",
                str(ctx.exception),
            )

    def test_validate_node_selector_mappings(self) -> None:
        head = SimpleNamespace(name="head", node_selector={"pool": "cpups"})
        worker = SimpleNamespace(name="worker", node_selector={"pool": "gpu-pool"})
        wu = SimpleNamespace(
            executable=SimpleNamespace(
                jobset=SimpleNamespace(jobs=[head, worker]),
                HasField=lambda f: f == "jobset",
            )
        )
        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
        res = xmc_entities_validator.validate_node_selector_mappings(
            stub, "20", {"head": {"pool": "cpups"}, "worker": {"pool": "gpu-pool"}}
        )
        self.assertEqual(res, {"head": {"pool": "cpups"}, "worker": {"pool": "gpu-pool"}})
        with self.assertRaisesRegex(AssertionError, "node_selector\\['pool'\\] mismatch"):
            xmc_entities_validator.validate_node_selector_mappings(
                stub, "20", {"head": {"pool": "wrong"}}
            )
        with self.assertRaisesRegex(AssertionError, "Job 'missing' missing from JobSet jobs"):
            xmc_entities_validator.validate_node_selector_mappings(
                stub, "20", {"missing": {"pool": "cpups"}}
            )

    def test_stop_work_units(self) -> None:
        stub = mock.MagicMock()
        xmc_entities_validator.stop_work_units(stub, "20")
        stub.BatchStopWorkUnits.assert_called_once()

    def test_normalize_dashboard_endpoint_empty_raises(self) -> None:
        for invalid in ("", "   ", "/", "  ///  "):
            with self.subTest(invalid=invalid):
                with self.assertRaisesRegex(ValueError, "cannot be empty or whitespace-only"):
                    xmc_entities_validator._normalize_dashboard_endpoint(invalid)

    def test_validate_dashboard_fatal_error_fails_fast(self) -> None:
        err_fp = io.BytesIO(b"data engine is not configured")
        http_err = urllib.error.HTTPError("http://localhost:8080", 500, "Error", {}, err_fp)  # type: ignore
        with mock.patch("urllib.request.urlopen", side_effect=http_err) as mock_open:
            with self.assertRaisesRegex(AssertionError, "data engine is not configured"):
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=["dash_1"],
                    timeout_sec=60,
                    poll_interval_sec=0,
                )
            self.assertEqual(mock_open.call_count, 1)

    def test_validate_dashboard_min_charts_assertion(self) -> None:
        mock_data = {"name": "dashboards/dash_1", "title": "VCC", "chart_ids": ["c1"]}
        with mock.patch(
            "tools.nightly_tests.verification.xmc_entities_validator.query_dashboard_http",
            return_value=mock_data,
        ):
            with self.assertRaisesRegex(AssertionError, "expected at least 3 constituent charts"):
                xmc_entities_validator.validate_dashboard(
                    dashboard_endpoint="localhost:8080",
                    expected_dashboards=[{"dashboard_id": "dash_1", "min_charts": 3}],
                    timeout_sec=0,
                    poll_interval_sec=0,
                )

    def _make_multikueue_meta(
        self,
        worker: str = "xmc-nightly-worker-1",
        exec_name: str = "xmc-nightly-worker-1",
        mgmt_name: str = "xmc-v2-nightly",
        exec_loc: str = "us-central1",
    ) -> Any:
        return SimpleNamespace(
            multi_kueue_cluster_name=worker,
            executing_cluster=SimpleNamespace(
                cluster_name=exec_name, project="test-proj", location=exec_loc
            ),
            management_cluster=SimpleNamespace(
                cluster_name=mgmt_name, project="test-proj", location="us-central1"
            ),
        )

    def test_validate_multikueue_execution_metadata_cached(self) -> None:
        wu = SimpleNamespace(
            name="experiments/50/workUnits/1",
            current_work_unit_execution=SimpleNamespace(metadata=self._make_multikueue_meta()),
        )
        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
        res = xmc_entities_validator.validate_multikueue_execution_metadata(
            stub, "50", expected_worker_cluster="xmc-nightly-worker-1"
        )
        self.assertEqual(
            res,
            [
                {
                    "work_unit": "experiments/50/workUnits/1",
                    "executing": "xmc-nightly-worker-1",
                    "management": "xmc-v2-nightly",
                }
            ],
        )
        stub.GetCurrentWorkUnitExecution.assert_not_called()

    def test_validate_multikueue_execution_metadata_rpc_fallback(self) -> None:
        wu = SimpleNamespace(
            name="experiments/50/workUnits/1",
            current_work_unit_execution=SimpleNamespace(
                metadata=SimpleNamespace(multi_kueue_cluster_name="")
            ),
        )
        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
        stub.GetCurrentWorkUnitExecution.return_value = SimpleNamespace(
            metadata=self._make_multikueue_meta()
        )
        res = xmc_entities_validator.validate_multikueue_execution_metadata(
            stub, "50", expected_worker_cluster="xmc-nightly-worker-1"
        )
        self.assertEqual(res[0]["executing"], "xmc-nightly-worker-1")
        stub.GetCurrentWorkUnitExecution.assert_called_once()

    def test_validate_multikueue_execution_metadata_invalid_cases(self) -> None:
        cases = [
            ("wrong_cluster", self._make_multikueue_meta(), "wrong-cluster", "invalid MultiKueue"),
            (
                "same_cluster",
                self._make_multikueue_meta("c1", "c1", "c1"),
                "c1",
                "invalid MultiKueue",
            ),
            (
                "missing_loc",
                self._make_multikueue_meta(exec_loc=""),
                "xmc-nightly-worker-1",
                "missing executing_cluster",
            ),
        ]
        for label, meta, expected, err_pat in cases:
            with self.subTest(case=label), self.assertRaisesRegex(AssertionError, err_pat):
                wu = SimpleNamespace(
                    name="experiments/50/workUnits/1",
                    current_work_unit_execution=SimpleNamespace(metadata=meta),
                )
                stub = mock.MagicMock()
                stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])
                xmc_entities_validator.validate_multikueue_execution_metadata(stub, "50", expected)

    def test_validate_multikueue_execution_metadata_empty_work_units(self) -> None:
        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[])
        with self.assertRaisesRegex(AssertionError, "No WorkUnits found for experiment 50"):
            xmc_entities_validator.validate_multikueue_execution_metadata(
                stub, "50", expected_worker_cluster="xmc-nightly-worker-1"
            )

    def test_validate_multikueue_k8s_delegation_success(self) -> None:
        responses = [
            SimpleNamespace(returncode=0, stdout="kueue.x-k8s.io/multikueue", stderr=""),
            SimpleNamespace(returncode=0, stdout="", stderr=""),
            SimpleNamespace(returncode=0, stdout="event/xmc-operation-50-1.1234", stderr=""),
        ]
        with mock.patch("subprocess.run", side_effect=responses) as mock_run:
            xmc_entities_validator.validate_multikueue_k8s_delegation("50", "multikueue-cpu-queue")
            self.assertEqual(mock_run.call_count, 3)

    def test_validate_multikueue_k8s_delegation_failures(self) -> None:
        def _ok(out: str) -> Any:
            return SimpleNamespace(returncode=0, stdout=out, stderr="")

        fail = SimpleNamespace(returncode=1, stdout="", stderr="connection refused")
        cases = [
            ("kubectl_err", [fail], "rc=1.*connection refused"),
            ("bad_managed_by", [_ok("kueue.x-k8s.io/jobset")], "Expected JobSets managedBy"),
            ("local_pods", [_ok("kueue.x-k8s.io/multikueue"), _ok("pod/p-0")], "Expected 0 local"),
            (
                "no_worker",
                [_ok("kueue.x-k8s.io/multikueue"), _ok(""), _ok("none")],
                "Missing worker",
            ),
        ]
        for label, side_effect, err_pat in cases:
            with self.subTest(case=label), mock.patch("subprocess.run", side_effect=side_effect):
                with self.assertRaisesRegex(AssertionError, err_pat):
                    xmc_entities_validator.validate_multikueue_k8s_delegation("50")

    def test_build_dashboard_headers_uses_cached_cli_token_fallback(self) -> None:
        url = "https://xmc.example.com/v1/dashboards/d1"
        with (
            mock.patch.dict("os.environ", {}, clear=True),
            mock.patch.object(
                xmc_entities_validator.xdash_auth,
                "load_cached_cli_token",
                return_value="cached-cli-tok",
            ) as mock_cli_tok,
        ):
            headers = xmc_entities_validator._build_dashboard_headers(url)
        mock_cli_tok.assert_called_once_with(url)
        self.assertEqual(headers["Authorization"], "Bearer cached-cli-tok")


if __name__ == "__main__":
    unittest.main()
