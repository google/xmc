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

"""Unit tests for example verification CLI scripts and common utilities."""

from __future__ import annotations

import os
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import (
    common,
    simple_job_verify,
    xmc_entities_validator,
)


class CommonUtilitiesTest(unittest.TestCase):
    """Hermetic unit tests for common.py helper functions."""

    def test_parse_xid_numeric(self) -> None:
        self.assertEqual(common.parse_xid("100"), "100")
        self.assertEqual(common.parse_xid(" 42 "), "42")

    def test_parse_xid_prefixed(self) -> None:
        self.assertEqual(common.parse_xid("experiments/100"), "100")
        self.assertEqual(common.parse_xid("dashboard:my_slug"), "my_slug")
        self.assertEqual(common.parse_xid("dashboards/my_slug"), "my_slug")

    def test_parse_xid_empty_and_required(self) -> None:
        self.assertEqual(common.parse_xid("", required=False), "")
        self.assertEqual(common.parse_xid(None, required=False), "")
        with self.assertRaises(ValueError):
            common.parse_xid("", required=True)
        with self.assertRaises(ValueError):
            common.parse_xid(None, required=True)

    def test_get_flag(self) -> None:
        obj = SimpleNamespace(xid="10", empty=None)
        self.assertEqual(common.get_flag(obj, "xid", "default"), "10")
        self.assertEqual(common.get_flag(obj, "empty", "default"), "default")
        self.assertEqual(common.get_flag(obj, "missing", "default"), "default")
        self.assertEqual(common.get_flag(None, "any", "default"), "default")

    def test_validate_argv(self) -> None:
        common.validate_argv(["script_name"])
        with self.assertRaises(common.app.UsageError):
            common.validate_argv(["script_name", "extra_arg"])

    def test_resolve_endpoint(self) -> None:
        self.assertEqual(common.resolve_endpoint("custom:1234"), "custom:1234")
        with mock.patch.dict(os.environ, {"XMANAGER_ENDPOINT": "env:5678"}):
            self.assertEqual(common.resolve_endpoint(None), "env:5678")
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(common.resolve_endpoint(None), "localhost:8080")


class SimpleJobVerifyTest(unittest.TestCase):
    """Hermetic unit tests for simple_job_verify script."""

    def _mock_work_unit(
        self,
        worker_name: str,
        queue_name: str = "cpu-queue",
        priority: int = 1,
        cpu_millicores: int = 500,
        memory_bytes: int = 512 * 1024 * 1024,
    ) -> SimpleNamespace:
        resources = SimpleNamespace(
            cpu_resources=SimpleNamespace(
                cpu_millicores=cpu_millicores,
                memory_bytes=memory_bytes,
            ),
            gpu_resources=None,
        )
        spec = SimpleNamespace(
            name=worker_name,
            docker_image="python:3.11-slim",
            resources=resources,
        )
        job = SimpleNamespace(
            name=worker_name,
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=spec,
        )
        executable = SimpleNamespace(
            scheduling_constraints=SimpleNamespace(queue_name=queue_name),
            jobset=SimpleNamespace(jobs=[job]),
            HasField=lambda f: f in ("scheduling_constraints", "jobset"),
        )
        return SimpleNamespace(
            name=f"experiments/100/workUnits/{worker_name}",
            priority=priority,
            notes=f"Demo workload for {worker_name} targeted at {queue_name}",
            tags=["60s-workload", queue_name, worker_name],
            executable=executable,
            current_work_unit_execution=SimpleNamespace(
                status=SimpleNamespace(state=xmc_entities_validator.STATE_COMPLETED),
                HasField=lambda f: f == "status",
            ),
        )

    def test_simple_job_verify_success(self) -> None:
        stub = mock.MagicMock()
        channel = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/100",
            title="Demo Workload (3 units, 60s execution)",
        )
        wus = [self._mock_work_unit(f"demo-worker-{i}", "cpu-queue") for i in range(1, 4)]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=wus)

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_work_units=3,
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(simple_job_verify, "FLAGS", flags_mock),
        ):
            simple_job_verify.main(["simple_job_verify"])
            channel.close.assert_called_once()

    def test_simple_job_verify_mismatched_priority(self) -> None:
        stub = mock.MagicMock()
        channel = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/100",
            title="Demo Workload (3 units, 60s execution)",
        )
        # priority 0 instead of 1 (HIGH)
        wus = [
            self._mock_work_unit(f"demo-worker-{i}", "cpu-queue", priority=0) for i in range(1, 4)
        ]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=wus)

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_work_units=3,
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(simple_job_verify, "FLAGS", flags_mock),
            self.assertRaises(SystemExit),
        ):
            simple_job_verify.main(["simple_job_verify"])


if __name__ == "__main__":
    unittest.main()
