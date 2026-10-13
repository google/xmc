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

"""Unit tests for tools.nightly_tests.multikueue_job_verify."""

from __future__ import annotations

import subprocess
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import multikueue_job_verify


def _make_valid_wu(
    name: str = "experiments/50/workUnits/1",
    worker_cluster: str = "xmc-nightly-worker-1",
    mgmt_cluster: str = "xmc-v2-nightly",
    has_exec: bool = True,
) -> SimpleNamespace:
    meta = SimpleNamespace(
        multi_kueue_cluster_name=worker_cluster,
        executing_cluster=SimpleNamespace(
            project="xmc-dev", location="us-central1", cluster_name=worker_cluster
        ),
        management_cluster=SimpleNamespace(
            project="xmc-dev", location="us-central1", cluster_name=mgmt_cluster
        ),
    )
    return SimpleNamespace(
        name=name,
        current_work_unit_execution=SimpleNamespace(metadata=meta),
        HasField=lambda f: has_exec and f == "current_work_unit_execution",
    )


class MultikueueJobVerifyTest(unittest.TestCase):
    """Tests for multikueue_job_verify explicit assertions."""

    def setUp(self) -> None:
        super().setUp()
        multikueue_job_verify.verifier_cli.FLAGS.mark_as_parsed()

    def test_assert_wu_multikueue_metadata_success_and_rpc_fallback(self) -> None:
        stub = mock.MagicMock()
        wu_cached = _make_valid_wu(has_exec=True)
        multikueue_job_verify._assert_wu_multikueue_metadata(
            stub, wu_cached, "xmc-nightly-worker-1"
        )
        stub.GetCurrentWorkUnitExecution.assert_not_called()

        wu_fallback = _make_valid_wu(has_exec=False)
        stub.GetCurrentWorkUnitExecution.return_value = wu_fallback.current_work_unit_execution
        multikueue_job_verify._assert_wu_multikueue_metadata(
            stub, wu_fallback, "xmc-nightly-worker-1"
        )
        stub.GetCurrentWorkUnitExecution.assert_called_once()

    def test_assert_wu_multikueue_metadata_failures(self) -> None:
        stub = mock.MagicMock()
        with self.assertRaisesRegex(AssertionError, "invalid MultiKueue metadata"):
            multikueue_job_verify._assert_wu_multikueue_metadata(
                stub, _make_valid_wu(worker_cluster="wrong-worker"), "xmc-nightly-worker-1"
            )
        with self.assertRaisesRegex(AssertionError, "invalid MultiKueue metadata"):
            multikueue_job_verify._assert_wu_multikueue_metadata(
                stub,
                _make_valid_wu(
                    worker_cluster="xmc-nightly-worker-1", mgmt_cluster="xmc-nightly-worker-1"
                ),
                "xmc-nightly-worker-1",
            )
        wu_missing_proj = _make_valid_wu()
        wu_missing_proj.current_work_unit_execution.metadata.executing_cluster.project = ""
        with self.assertRaisesRegex(AssertionError, "invalid MultiKueue metadata"):
            multikueue_job_verify._assert_wu_multikueue_metadata(
                stub, wu_missing_proj, "xmc-nightly-worker-1"
            )

    def test_verify_k8s_multikueue_crs(self) -> None:
        ok_active = subprocess.CompletedProcess([], 0, stdout="True\n", stderr="")
        ok_wl = subprocess.CompletedProcess(
            [], 0, stdout="xmc-nightly-worker-1 xmc-nightly-worker-1\n", stderr=""
        )
        with mock.patch.object(
            multikueue_job_verify,
            "_run_kubectl",
            side_effect=[ok_active, ok_active, ok_wl],
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs(
                "50", "multikueue-cpu-queue", "xmc-nightly-worker-1", 2
            )

        bad_active = subprocess.CompletedProcess([], 0, stdout="False\n", stderr="not ready")
        with (
            mock.patch.object(multikueue_job_verify, "_run_kubectl", return_value=bad_active),
            self.assertRaisesRegex(AssertionError, "condition Active=True failed"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs(
                "50", "multikueue-cpu-queue", "xmc-nightly-worker-1", 2
            )

        bad_wl = subprocess.CompletedProcess([], 0, stdout="xmc-nightly-worker-1\n", stderr="")
        with (
            mock.patch.object(
                multikueue_job_verify,
                "_run_kubectl",
                side_effect=[ok_active, ok_active, bad_wl],
            ),
            self.assertRaisesRegex(AssertionError, "Workload.status.clusterName"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs(
                "50", "multikueue-cpu-queue", "xmc-nightly-worker-1", 2
            )

    def test_assert_multikueue_workload_and_main(self) -> None:
        with mock.patch.object(
            multikueue_job_verify.workload_verification, "verify_standard_workload"
        ) as m_verify:
            multikueue_job_verify.main(["multikueue_job_verify.py"])
            m_verify.assert_called_once()

        stub = mock.MagicMock()
        stub.ListWorkUnits.return_value = SimpleNamespace(
            work_units=[_make_valid_wu(f"experiments/50/workUnits/{i}") for i in (1, 2, 3)]
        )
        with (
            mock.patch.object(
                multikueue_job_verify.simple_job_verify, "_assert_all_work_unit_specs"
            ) as m_spec,
            mock.patch.object(multikueue_job_verify, "_verify_k8s_multikueue_crs") as m_k8s,
        ):
            multikueue_job_verify._assert_multikueue_workload(stub, "50")
            m_spec.assert_called_once_with(stub, "50")
            m_k8s.assert_called_once_with("50", "multikueue-cpu-queue", "xmc-nightly-worker-1", 3)


if __name__ == "__main__":
    unittest.main()
