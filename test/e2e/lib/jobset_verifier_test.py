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

"""Unit tests for test.e2e.lib.jobset_verifier."""

from __future__ import annotations

import copy
import json
import subprocess
import unittest
from typing import Any
from unittest import mock

from test.e2e.lib.jobset_verifier import (
    E2E_IMAGE,
    assert_jobset_contract,
    build_work_unit_payload,
    wait_for_jobset_for_work_unit,
    wait_for_jobsets_deleted,
    wait_for_workload_admitted,
    wait_for_workload_preempted,
)
from test.e2e.lib.polling import PollTimeoutError

_WORK_UNIT = "experiments/42/workUnits/1"
_WORK_UNIT_SELECTOR = "xmc.google.com/experiment-id=42,xmc.google.com/work-unit-id=1"


def _kubectl_items(items: list[dict[str, Any]]) -> subprocess.CompletedProcess[str]:
    """Returns a successful `kubectl get -o json` result listing items."""
    return subprocess.CompletedProcess(
        args=[], returncode=0, stdout=json.dumps({"items": items}), stderr=""
    )


def _workload(owner_uid: str, admitted: str) -> dict[str, Any]:
    """Returns a Workload owned by owner_uid whose Admitted condition has the given status."""
    return {
        "metadata": {"ownerReferences": [{"uid": owner_uid}]},
        "status": {
            "conditions": [
                {"type": "QuotaReserved", "status": "True"},
                {"type": "Admitted", "status": admitted},
            ],
        },
    }


def _evicted_workload(owner_uid: str, reason: str, count: int) -> dict[str, Any]:
    """Returns an admitted Workload whose scheduling statistics count evictions for reason."""
    workload = _workload(owner_uid, "True")
    workload["status"]["schedulingStats"] = {"evictions": [{"reason": reason, "count": count}]}
    return workload


def _baseline_jobset() -> dict[str, Any]:
    """Returns a valid baseline JobSet dictionary satisfying the XMC contract."""
    return {
        "metadata": {
            "name": "xmc-operation-42-1",
            "namespace": "cpu-queue",
            "uid": "jobset-uid",
            "labels": {
                "kueue.x-k8s.io/queue-name": "cpu-queue",
                "app.kubernetes.io/managed-by": "xmc",
                "xmc.google.com/experiment-id": "42",
                "xmc.google.com/work-unit-id": "1",
            },
        },
        "spec": {
            "failurePolicy": {"maxRestarts": 0},
            "replicatedJobs": [
                {
                    "name": "worker",
                    "replicas": 1,
                    "template": {
                        "spec": {
                            "template": {
                                "spec": {
                                    "restartPolicy": "Never",
                                },
                            },
                        },
                    },
                },
            ],
        },
    }


class JobSetVerifierTest(unittest.TestCase):
    """Tests for JobSet verification helpers."""

    def test_build_work_unit_payload_sets_queue_and_resources(self) -> None:
        """build_work_unit_payload must populate schedulingConstraints and CPU resources."""
        payload = build_work_unit_payload("cpu-queue")
        self.assertEqual(payload["workUnitType"], "STANDARD")
        self.assertEqual(payload["priority"], "NORMAL")
        self.assertEqual(
            payload["executable"]["schedulingConstraints"]["queueName"],
            "cpu-queue",
        )
        jobs = payload["executable"]["jobset"]["jobs"]
        self.assertEqual(len(jobs), 1)
        self.assertEqual(jobs[0]["spec"]["resources"]["cpuResources"]["cpuMillicores"], 100)
        self.assertEqual(jobs[0]["spec"]["dockerImage"], E2E_IMAGE)
        self.assertEqual(jobs[0]["spec"]["command"], ["sh", "-c", "echo hello"])

    def test_build_work_unit_payload_runs_given_command(self) -> None:
        """build_work_unit_payload must run the given command instead of the default one."""
        payload = build_work_unit_payload("cpu-queue", command=("sh", "-c", "exit 50"))
        spec = payload["executable"]["jobset"]["jobs"][0]["spec"]
        self.assertEqual(spec["command"], ["sh", "-c", "exit 50"])

    def test_build_work_unit_payload_sets_priority_and_cpu(self) -> None:
        """build_work_unit_payload must request the given priority and CPU millicores."""
        payload = build_work_unit_payload("preemption-queue", priority="HIGH", cpu_millicores=400)
        self.assertEqual(payload["priority"], "HIGH")
        resources = payload["executable"]["jobset"]["jobs"][0]["spec"]["resources"]
        self.assertEqual(
            resources["cpuResources"], {"cpuMillicores": 400, "memoryBytes": 134217728}
        )

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_polls_until_present(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_work_unit retries when items is empty and returns first match."""
        mock_run.side_effect = [_kubectl_items([]), _kubectl_items([_baseline_jobset()])]

        got = wait_for_jobset_for_work_unit(
            _WORK_UNIT,
            namespace="cpu-queue",
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got["metadata"]["name"], "xmc-operation-42-1")
        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once()
        command = mock_run.call_args.args[0]
        self.assertEqual(command[command.index("-l") + 1], _WORK_UNIT_SELECTOR)

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.polling.time.monotonic")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_raises_on_timeout(
        self,
        mock_run: mock.MagicMock,
        mock_monotonic: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_work_unit logs actuator output and raises at the deadline."""
        mock_monotonic.side_effect = [0.0, 10.0]
        mock_run.return_value = _kubectl_items([])

        with self.assertRaisesRegex(PollTimeoutError, "Timed out"):
            wait_for_jobset_for_work_unit(
                _WORK_UNIT,
                kube_context="kind-xmc-e2e",
                timeout_seconds=5.0,
            )
        mock_sleep.assert_not_called()
        self.assertIn("logs", mock_run.call_args.args[0])

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_retries_on_transient_kubectl_error(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_work_unit retries when kubectl fails or times out."""
        mock_run.side_effect = [
            subprocess.CalledProcessError(returncode=1, cmd=["kubectl"], stderr="conn refused"),
            subprocess.TimeoutExpired(cmd=["kubectl"], timeout=10.0),
            _kubectl_items([_baseline_jobset()]),
        ]

        got = wait_for_jobset_for_work_unit(
            _WORK_UNIT,
            namespace="cpu-queue",
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got["metadata"]["name"], "xmc-operation-42-1")
        self.assertEqual(mock_run.call_count, 3)
        self.assertEqual(mock_sleep.call_count, 2)

    def test_wait_for_jobset_rejects_invalid_work_unit_name(self) -> None:
        """wait_for_jobset_for_work_unit rejects empty or malformed work unit names."""
        bad_names = ("", "invalid-name", "experiments/42", "experiments/42/workUnits/")
        for bad_name in bad_names + ("experiments//workUnits/1",):
            with self.subTest(bad_name=bad_name):
                with self.assertRaises(ValueError):
                    wait_for_jobset_for_work_unit(bad_name)

    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_selects_given_execution(self, mock_run: mock.MagicMock) -> None:
        """wait_for_jobset_for_work_unit only matches the JobSet of the given execution."""
        mock_run.return_value = _kubectl_items([_baseline_jobset()])

        wait_for_jobset_for_work_unit(_WORK_UNIT, execution_id=2, kube_context="kind-xmc-e2e")

        command = mock_run.call_args.args[0]
        self.assertEqual(
            command[command.index("-l") + 1],
            f"{_WORK_UNIT_SELECTOR},xmc.google.com/execution-id=2",
        )

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobsets_deleted_polls_until_none_remain(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobsets_deleted retries while any JobSet of the work unit exists."""
        mock_run.side_effect = [_kubectl_items([_baseline_jobset()]), _kubectl_items([])]

        wait_for_jobsets_deleted(
            _WORK_UNIT,
            namespace="preemption-queue",
            kube_context="kind-xmc-e2e",
            poll_interval_seconds=0.01,
        )

        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once()
        command = mock_run.call_args.args[0]
        self.assertEqual(command[command.index("-l") + 1], _WORK_UNIT_SELECTOR)
        self.assertEqual(command[command.index("-n") + 1], "preemption-queue")

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.polling.time.monotonic")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobsets_deleted_raises_on_timeout(
        self,
        mock_run: mock.MagicMock,
        mock_monotonic: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobsets_deleted raises when a JobSet outlives the deadline."""
        mock_monotonic.side_effect = [0.0, 10.0]
        mock_run.return_value = _kubectl_items([_baseline_jobset()])

        with self.assertRaisesRegex(PollTimeoutError, "deletion of the JobSets"):
            wait_for_jobsets_deleted(_WORK_UNIT, kube_context="kind-xmc-e2e", timeout_seconds=5.0)
        mock_sleep.assert_not_called()

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_workload_admitted_polls_until_admitted(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_workload_admitted skips other owners' and unadmitted Workloads."""
        mock_run.side_effect = [
            _kubectl_items([_workload("other-uid", "True"), _workload("jobset-uid", "False")]),
            _kubectl_items([_workload("other-uid", "True"), _workload("jobset-uid", "True")]),
        ]

        got = wait_for_workload_admitted(
            _baseline_jobset(),
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got, _workload("jobset-uid", "True"))
        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once()
        command = mock_run.call_args.args[0]
        self.assertIn("workloads.kueue.x-k8s.io", command)
        self.assertEqual(command[command.index("-n") + 1], "cpu-queue")
        self.assertNotIn("-l", command)

    def test_wait_for_workload_admitted_rejects_jobset_without_uid(self) -> None:
        """wait_for_workload_admitted needs the JobSet UID to find its Workload."""
        jobset = _baseline_jobset()
        del jobset["metadata"]["uid"]
        with self.assertRaisesRegex(ValueError, "UID"):
            wait_for_workload_admitted(jobset)

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_workload_preempted_polls_until_preemption_counted(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_workload_preempted ignores other owners, eviction reasons, and zero counts."""
        mock_run.side_effect = [
            _kubectl_items([_workload("jobset-uid", "True")]),
            _kubectl_items([_evicted_workload("jobset-uid", "PodsReadyTimeout", 1)]),
            _kubectl_items([_evicted_workload("jobset-uid", "Preempted", 0)]),
            _kubectl_items(
                [
                    _evicted_workload("other-uid", "Preempted", 1),
                    _evicted_workload("jobset-uid", "Preempted", 1),
                ]
            ),
        ]

        got = wait_for_workload_preempted(
            _baseline_jobset(),
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got, _evicted_workload("jobset-uid", "Preempted", 1))
        self.assertEqual(mock_run.call_count, 4)
        self.assertEqual(mock_sleep.call_count, 3)

    def test_assert_jobset_contract_accepts_valid_jobset(self) -> None:
        """assert_jobset_contract succeeds on a compliant JobSet and rejects empty dicts."""
        assert_jobset_contract(_baseline_jobset())
        jobset_null_fp = copy.deepcopy(_baseline_jobset())
        jobset_null_fp["spec"]["failurePolicy"] = None
        assert_jobset_contract(jobset_null_fp)
        with self.assertRaises(ValueError):
            assert_jobset_contract({})

    def test_assert_jobset_contract_rejects_violations(self) -> None:
        """assert_jobset_contract raises RuntimeError for each contract violation."""
        bad_queue = copy.deepcopy(_baseline_jobset())
        bad_queue["metadata"]["labels"]["kueue.x-k8s.io/queue-name"] = "wrong"

        bad_managed_by = copy.deepcopy(_baseline_jobset())
        bad_managed_by["metadata"]["labels"]["app.kubernetes.io/managed-by"] = "other"

        bad_restarts = copy.deepcopy(_baseline_jobset())
        bad_restarts["spec"]["failurePolicy"]["maxRestarts"] = 1

        empty_jobs = copy.deepcopy(_baseline_jobset())
        empty_jobs["spec"]["replicatedJobs"] = []

        bad_policy = copy.deepcopy(_baseline_jobset())
        bad_policy["spec"]["replicatedJobs"][0]["template"]["spec"]["template"]["spec"][
            "restartPolicy"
        ] = "OnFailure"

        null_template = copy.deepcopy(_baseline_jobset())
        null_template["spec"]["replicatedJobs"][0]["template"] = None

        cases = [
            ("queue label", bad_queue, "kueue.x-k8s.io/queue-name"),
            ("managed-by label", bad_managed_by, "managed-by"),
            ("maxRestarts", bad_restarts, "maxRestarts"),
            ("empty replicatedJobs", empty_jobs, "replicatedJobs"),
            ("restartPolicy", bad_policy, "restartPolicy"),
            ("null template", null_template, "restartPolicy"),
        ]
        for name, candidate, expected_msg in cases:
            with self.subTest(name=name):
                with self.assertRaisesRegex(RuntimeError, expected_msg):
                    assert_jobset_contract(candidate)


if __name__ == "__main__":
    unittest.main()
