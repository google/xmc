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
                    "Failed to retrieve current WorkUnitExecution for experiments/10/workUnits/3"
                    in msg
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

    def test_validate_work_units_custom_workers(self) -> None:
        stub = mock.MagicMock()
        job = SimpleNamespace(name="custom-trainer")
        jobset = SimpleNamespace(jobs=[job])
        constraints = SimpleNamespace(queue_name="tpu-queue")
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
            tags=["tpu-queue", "custom-trainer"],
            current_work_unit_execution=execution,
            HasField=lambda f: f == "current_work_unit_execution",
        )
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=1,
            expected_queue="tpu-queue",
            expected_worker_names=["custom-trainer"],
            timeout_sec=10,
            wait_for_completion=True,
        )
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["worker_name"], "custom-trainer")

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

    # =========================================================================
    # Phase 1: validate_jobsets Tests
    # =========================================================================

    def _create_mock_jobset_wu(self, jobs: list[SimpleNamespace]) -> SimpleNamespace:
        jobset = SimpleNamespace(jobs=jobs)
        executable = SimpleNamespace(
            jobset=jobset,
            HasField=lambda f: f == "jobset",
        )
        return SimpleNamespace(
            name="experiments/10/workUnits/1",
            executable=executable,
        )

    def test_validate_jobsets_success_single_job_with_init_and_sidecars(self) -> None:
        init_c = SimpleNamespace(name="init-setup", docker_image="gcr.io/init:v1")
        sidecar_c = SimpleNamespace(name="sidecar-service", docker_image="gcr.io/sidecar:v1")
        primary_c = SimpleNamespace(name="primary-worker", docker_image="gcr.io/worker:v1")
        job = SimpleNamespace(
            name="sidecar-worker",
            replicas=1,
            init_containers=[init_c],
            sidecars=[sidecar_c],
            spec=primary_c,
        )
        wu = self._create_mock_jobset_wu([job])

        expected_jobs = [
            {
                "name": "sidecar-worker",
                "replicas": 1,
                "init_containers": ["init-setup"],
                "sidecars": [{"name": "sidecar-service", "docker_image": "gcr.io/sidecar:v1"}],
                "spec": "primary-worker",
            }
        ]
        results = xmc_entities_validator.validate_jobsets(wu, expected_jobs)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["name"], "sidecar-worker")
        self.assertEqual(results[0]["replicas"], 1)
        self.assertEqual(results[0]["init_containers"][0]["name"], "init-setup")
        self.assertEqual(results[0]["sidecars"][0]["name"], "sidecar-service")
        self.assertEqual(results[0]["primary_container"], "primary-worker")

    def test_validate_jobsets_success_multipod_job(self) -> None:
        job1 = SimpleNamespace(
            name="counter-workers",
            replicas=2,
            init_containers=[],
            sidecars=[SimpleNamespace(name="sidecar-counter", docker_image="gcr.io/img:latest")],
            spec=SimpleNamespace(name="primary-counter", docker_image="gcr.io/img:latest"),
        )
        job2 = SimpleNamespace(
            name="secondary-counter",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="secondary-counter", docker_image="gcr.io/img:latest"),
        )
        wu = self._create_mock_jobset_wu([job1, job2])

        expected_jobs = [
            {
                "name": "counter-workers",
                "replicas": 2,
                "sidecars": ["sidecar-counter"],
                "primary_container": {
                    "name": "primary-counter",
                    "docker_image": "gcr.io/img:latest",
                },
            },
            {
                "name": "secondary-counter",
                "replicas": 1,
                "spec": "secondary-counter",
            },
        ]
        results = xmc_entities_validator.validate_jobsets(wu, expected_jobs)
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0]["name"], "counter-workers")
        self.assertEqual(results[1]["name"], "secondary-counter")

    def test_validate_jobsets_integer_counts(self) -> None:
        init_c1 = SimpleNamespace(name="init-1", docker_image="")
        init_c2 = SimpleNamespace(name="init-2", docker_image="")
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[init_c1, init_c2],
            sidecars=[],
            spec=SimpleNamespace(name="main"),
        )
        wu = self._create_mock_jobset_wu([job])

        expected_jobs = [
            {
                "name": "worker",
                "init_containers": 2,
                "sidecars": 0,
            }
        ]
        results = xmc_entities_validator.validate_jobsets(wu, expected_jobs)
        self.assertEqual(len(results), 1)

    def test_validate_jobsets_missing_executable(self) -> None:
        wu = SimpleNamespace(name="experiments/10/workUnits/1")
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "worker"}])
        self.assertIn("missing executable field", str(ctx.exception))

    def test_validate_jobsets_not_a_jobset(self) -> None:
        executable = SimpleNamespace(
            HasField=lambda f: False,
        )
        wu = SimpleNamespace(name="experiments/10/workUnits/1", executable=executable)
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "worker"}])
        self.assertIn("does not contain a JobSet executable", str(ctx.exception))

    def test_validate_jobsets_count_mismatch(self) -> None:
        job = SimpleNamespace(name="worker", replicas=1, init_containers=[], sidecars=[])
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "w1"}, {"name": "w2"}])
        self.assertIn("JobSet job count mismatch: expected 2, found 1", str(ctx.exception))

    def test_validate_jobsets_job_name_missing_in_spec(self) -> None:
        job = SimpleNamespace(name="worker", replicas=1, init_containers=[], sidecars=[])
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"replicas": 1}])
        self.assertIn("must specify a 'name'", str(ctx.exception))

    def test_validate_jobsets_positional_fallback(self) -> None:
        job = SimpleNamespace(name="actual-worker", replicas=1, init_containers=[], sidecars=[])
        wu = self._create_mock_jobset_wu([job])
        results = xmc_entities_validator.validate_jobsets(wu, [{"name": "fallback-worker"}])
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["name"], "actual-worker")

    def test_validate_jobsets_replicas_mismatch(self) -> None:
        job = SimpleNamespace(name="worker", replicas=1, init_containers=[], sidecars=[])
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "worker", "replicas": 4}])
        self.assertIn("replica count mismatch: expected 4, found 1", str(ctx.exception))

    def test_validate_jobsets_init_container_count_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[SimpleNamespace(name="init-1", docker_image="")],
            sidecars=[],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "worker", "init_containers": 3}])
        self.assertIn("init container count mismatch", str(ctx.exception))

    def test_validate_jobsets_init_container_name_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[SimpleNamespace(name="init-actual", docker_image="")],
            sidecars=[],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu, [{"name": "worker", "init_containers": ["init-expected"]}]
            )
        self.assertIn(
            "init container name mismatch: expected 'init-expected', found 'init-actual'",
            str(ctx.exception),
        )

    def test_validate_jobsets_init_container_property_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[SimpleNamespace(name="init-1", docker_image="gcr.io/v1")],
            sidecars=[],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu,
                [
                    {
                        "name": "worker",
                        "init_containers": [{"name": "init-1", "docker_image": "gcr.io/v2"}],
                    }
                ],
            )
        self.assertIn("init container property 'docker_image' mismatch", str(ctx.exception))

    def test_validate_jobsets_sidecar_count_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[SimpleNamespace(name="sidecar-1", docker_image="")],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu, [{"name": "worker", "sidecars": ["s1", "s2"]}]
            )
        self.assertIn("sidecar container count mismatch", str(ctx.exception))

    def test_validate_jobsets_sidecar_name_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[SimpleNamespace(name="sidecar-actual", docker_image="")],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu, [{"name": "worker", "sidecars": ["sidecar-expected"]}]
            )
        self.assertIn(
            "sidecar container name mismatch: expected 'sidecar-expected', found 'sidecar-actual'",
            str(ctx.exception),
        )

    def test_validate_jobsets_sidecar_property_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[SimpleNamespace(name="sidecar-1", docker_image="gcr.io/v1")],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu,
                [
                    {
                        "name": "worker",
                        "sidecars": [{"name": "sidecar-1", "docker_image": "gcr.io/v2"}],
                    }
                ],
            )
        self.assertIn("sidecar container property 'docker_image' mismatch", str(ctx.exception))

    def test_validate_jobsets_primary_spec_missing(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(wu, [{"name": "worker", "spec": "main"}])
        self.assertIn("missing primary container spec", str(ctx.exception))

    def test_validate_jobsets_primary_spec_name_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="actual-main"),
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu, [{"name": "worker", "spec": "expected-main"}]
            )
        self.assertIn("primary container name mismatch", str(ctx.exception))

    def test_validate_jobsets_primary_spec_property_mismatch(self) -> None:
        job = SimpleNamespace(
            name="worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="main", docker_image="gcr.io/v1"),
        )
        wu = self._create_mock_jobset_wu([job])
        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_jobsets(
                wu, [{"name": "worker", "spec": {"name": "main", "docker_image": "gcr.io/v2"}}]
            )
        self.assertIn("primary container property 'docker_image' mismatch", str(ctx.exception))

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
            payload=SimpleNamespace(chart=chart_obj, WhichOneof=lambda f: "chart"),
        )
        art_url = SimpleNamespace(
            name="artifacts/2",
            parent="experiments/10/workUnits/1",
            payload=SimpleNamespace(url=url_obj, WhichOneof=lambda f: "url"),
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
        self.assertEqual(results[0]["metric_names"], ["val/loss", "train/accuracy"])
        self.assertEqual(results[1]["type"], "url")
        self.assertEqual(results[1]["url"], "https://dashboard.example.com/view")

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

    # =========================================================================
    # Phase 2: validate_dashboard Tests
    # =========================================================================

    def test_validate_dashboard_success_with_charts(self) -> None:
        mock_dashboard_data = {
            "name": "dashboards/example_vcc",
            "title": "VCC Comparative Dashboard",
            "chart_ids": ["chart_1", "chart_2"],
        }
        mock_chart_1 = {"name": "charts/chart_1", "title": "Loss vs Steps"}
        mock_chart_2 = {"name": "charts/chart_2", "title": "Accuracy vs Steps"}

        def mock_urlopen(req: urllib.request.Request, timeout: int = 10) -> io.BytesIO:
            url = req.full_url
            if "/v1/dashboards/example_vcc" in url:
                resp = io.BytesIO(json.dumps(mock_dashboard_data).encode("utf-8"))
                resp.status = 200  # type: ignore
                return resp
            if "/v1/charts/chart_1" in url:
                resp = io.BytesIO(json.dumps(mock_chart_1).encode("utf-8"))
                resp.status = 200  # type: ignore
                return resp
            if "/v1/charts/chart_2" in url:
                resp = io.BytesIO(json.dumps(mock_chart_2).encode("utf-8"))
                resp.status = 200  # type: ignore
                return resp
            raise urllib.error.HTTPError(url, 404, "Not Found", {}, None)  # type: ignore

        with mock.patch("urllib.request.urlopen", side_effect=mock_urlopen):
            results = xmc_entities_validator.validate_dashboard(
                dashboard_endpoint="localhost:8080",
                expected_dashboards=[
                    {
                        "dashboard_id": "example_vcc",
                        "expected_title": "VCC Comparative",
                        "expected_chart_ids": ["chart_1", "chart_2"],
                    }
                ],
                timeout_sec=5,
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
            raise urllib.error.HTTPError(req.full_url, 500, "Internal Server Error", {}, None)

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


if __name__ == "__main__":
    unittest.main()
