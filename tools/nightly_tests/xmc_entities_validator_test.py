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

import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import xmc_entities_validator


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
        cases = [
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


if __name__ == "__main__":
    unittest.main()
