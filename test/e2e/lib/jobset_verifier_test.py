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
    assert_jobset_contract,
    build_work_unit_payload,
    wait_for_jobset_for_experiment,
)


def _baseline_jobset() -> dict[str, Any]:
    """Returns a valid baseline JobSet dictionary satisfying the XMC contract."""
    return {
        "metadata": {
            "name": "xmc-operation-42-1",
            "namespace": "cpu-queue",
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
        self.assertEqual(
            payload["executable"]["schedulingConstraints"]["queueName"],
            "cpu-queue",
        )
        jobs = payload["executable"]["jobset"]["jobs"]
        self.assertEqual(len(jobs), 1)
        self.assertEqual(jobs[0]["spec"]["resources"]["cpuResources"]["cpuMillicores"], 500)

    @mock.patch("test.e2e.lib.jobset_verifier.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_polls_until_present(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_experiment retries when items is empty and returns first match."""
        expected = _baseline_jobset()
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout='{"items": []}', stderr=""),
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"items": [expected]}),
                stderr="",
            ),
        ]

        got = wait_for_jobset_for_experiment(
            "experiments/42",
            namespace="cpu-queue",
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got["metadata"]["name"], "xmc-operation-42-1")
        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once()

    @mock.patch("test.e2e.lib.jobset_verifier.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.time.monotonic")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_raises_on_timeout(
        self,
        mock_run: mock.MagicMock,
        mock_monotonic: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_experiment raises RuntimeError when deadline expires."""
        mock_monotonic.side_effect = [0.0, 10.0]
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout='{"items": []}', stderr=""
        )

        with self.assertRaisesRegex(RuntimeError, "Timed out"):
            wait_for_jobset_for_experiment(
                "experiments/42",
                kube_context="kind-xmc-e2e",
                timeout_seconds=5.0,
            )
        mock_sleep.assert_not_called()

    @mock.patch("test.e2e.lib.jobset_verifier.time.sleep")
    @mock.patch("test.e2e.lib.jobset_verifier.subprocess.run")
    def test_wait_for_jobset_retries_on_transient_kubectl_error(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """wait_for_jobset_for_experiment retries when kubectl returns a transient error."""
        expected = _baseline_jobset()
        mock_run.side_effect = [
            subprocess.CalledProcessError(returncode=1, cmd=["kubectl"]),
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"items": [expected]}),
                stderr="",
            ),
        ]

        got = wait_for_jobset_for_experiment(
            "experiments/42",
            namespace="cpu-queue",
            kube_context="kind-xmc-e2e",
            timeout_seconds=5.0,
            poll_interval_seconds=0.01,
        )

        self.assertEqual(got["metadata"]["name"], "xmc-operation-42-1")
        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once()

    def test_wait_for_jobset_rejects_invalid_experiment_name(self) -> None:
        """wait_for_jobset_for_experiment rejects malformed experiment names."""
        with self.assertRaises(ValueError):
            wait_for_jobset_for_experiment("invalid-name")

    def test_assert_jobset_contract_accepts_valid_jobset(self) -> None:
        """assert_jobset_contract succeeds on a compliant JobSet and when failurePolicy is None."""
        assert_jobset_contract(_baseline_jobset())
        jobset_null_fp = copy.deepcopy(_baseline_jobset())
        jobset_null_fp["spec"]["failurePolicy"] = None
        assert_jobset_contract(jobset_null_fp)

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
