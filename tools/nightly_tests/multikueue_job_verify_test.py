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

"""Unit tests for tools.nightly_tests.multikueue_job_verify module."""

from __future__ import annotations

import os
import subprocess
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import multikueue_job_verify
from tools.nightly_tests.verification import verifier_cli


def _make_raw_multikueue_wu(index: int, cluster: str = "xmc-nightly-worker-1") -> SimpleNamespace:
    metadata = SimpleNamespace(
        multi_kueue_cluster_name=cluster,
        executing_cluster=SimpleNamespace(cluster_name=cluster),
        management_cluster=SimpleNamespace(cluster_name="xmc-v2-nightly"),
    )
    return SimpleNamespace(
        name=f"experiments/42/workUnits/{index}",
        current_work_unit_execution=SimpleNamespace(metadata=metadata),
        HasField=lambda f: f == "current_work_unit_execution",
    )


def _kubectl_side_effect(
    active_status: str = "True",
    cr_rc: int = 0,
    wl_stdout: str = "xmc-nightly-worker-1 xmc-nightly-worker-1",
    wl_rc: int = 0,
    ctx_rc: int = 0,
    ctx_stdout: str = "gke_xmc-nightly",
):
    def _run(cmd: list[str], **_kwargs):
        if cmd[:3] == ["kubectl", "config", "current-context"]:
            return subprocess.CompletedProcess(
                args=cmd, returncode=ctx_rc, stdout=ctx_stdout, stderr=""
            )
        if cmd[:3] == ["kubectl", "get", "workloads"]:
            return subprocess.CompletedProcess(
                args=cmd, returncode=wl_rc, stdout=wl_stdout, stderr="err"
            )
        return subprocess.CompletedProcess(
            args=cmd, returncode=cr_rc, stdout=active_status, stderr="NotFound"
        )

    return _run


class MultikueueJobVerifyTest(unittest.TestCase):
    """Test cases for multikueue_job_verify."""

    def test_assert_multikueue_work_units_success(self) -> None:
        stub = mock.MagicMock()
        work_units = [_make_raw_multikueue_wu(1), _make_raw_multikueue_wu(2)]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=work_units)
        mock_flags = SimpleNamespace(
            expected_queue="multikueue-cpu-queue",
            expected_worker_cluster="xmc-nightly-worker-1",
            expected_work_units=2,
        )
        with (
            mock.patch.object(verifier_cli, "FLAGS", mock_flags),
            mock.patch.object(subprocess, "run", side_effect=_kubectl_side_effect()),
        ):
            multikueue_job_verify._assert_multikueue_work_units(stub, "42")

    def test_verify_k8s_multikueue_crs_skips_when_no_kubectl_or_context(self) -> None:
        with mock.patch.object(subprocess, "run", side_effect=FileNotFoundError):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")
        with mock.patch.object(
            subprocess, "run", side_effect=_kubectl_side_effect(ctx_rc=1, ctx_stdout="")
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")

    def test_verify_k8s_multikueue_crs_fails_on_kubectl_get_error_or_inactive(self) -> None:
        with (
            mock.patch.object(subprocess, "run", side_effect=_kubectl_side_effect(cr_rc=1)),
            self.assertRaisesRegex(AssertionError, "condition Active=True"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")
        with (
            mock.patch.object(
                subprocess, "run", side_effect=_kubectl_side_effect(active_status="False")
            ),
            self.assertRaisesRegex(AssertionError, "condition Active=True"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")

    def test_verify_k8s_multikueue_crs_fails_on_workload_cluster_mismatch(self) -> None:
        with (
            mock.patch.object(
                subprocess, "run", side_effect=_kubectl_side_effect(wl_stdout="wrong-cluster")
            ),
            self.assertRaisesRegex(AssertionError, "Workload.status.clusterName"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")
        with (
            mock.patch.object(subprocess, "run", side_effect=_kubectl_side_effect(wl_rc=1)),
            self.assertRaisesRegex(AssertionError, "Workload.status.clusterName"),
        ):
            multikueue_job_verify._verify_k8s_multikueue_crs("42")

    def test_main_invokes_verify_standard_workload(self) -> None:
        mock_flags = SimpleNamespace(
            expected_queue="cpu-queue",
            expected_worker_cluster="xmc-nightly-worker-1",
            expected_work_units=2,
        )
        with (
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(verifier_cli, "FLAGS", mock_flags),
            mock.patch.object(verifier_cli, "resolve_expected_work_units", return_value=2),
            mock.patch(
                "tools.nightly_tests.verification.workload_verification.verify_standard_workload"
            ) as mock_verify,
        ):
            multikueue_job_verify.main(["multikueue_job_verify.py"])
            self.assertEqual(mock_flags.expected_queue, multikueue_job_verify.DEFAULT_QUEUE)
            mock_verify.assert_called_once_with(
                ["multikueue_job_verify.py"],
                banner_title="XMC MultiKueue Delegated Job Verification & Assertions",
                summary_title="ALL MULTIKUEUE JOB ASSERTIONS PASSED SUCCESSFULLY!",
                expected_title_pattern=multikueue_job_verify.EXPECTED_TITLE_PATTERN,
                expected_jobs=[
                    {
                        "name": "multikueue-worker-1",
                        "replicas": 1,
                        "primary_container": "primary-worker",
                    }
                ],
                expected_worker_names=["multikueue-worker-1", "multikueue-worker-2"],
                extra_step=multikueue_job_verify._assert_multikueue_work_units,
            )


if __name__ == "__main__":
    unittest.main()
