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
    """Test suite for basic xmc_entities_validator library."""

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

    def test_validate_experiment_success(self) -> None:
        stub = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/42",
            title="Demo Workload (3 units, 60s execution)",
            creator="test@google.com",
        )
        exp = xmc_entities_validator.validate_experiment(stub, "42")
        self.assertEqual(exp.name, "experiments/42")

    def _create_mock_work_unit(
        self, xid: str, index: int, worker_name: str | None = None
    ) -> SimpleNamespace:
        name = worker_name or f"demo-worker-{index}"
        job = SimpleNamespace(name=name)
        jobset = SimpleNamespace(jobs=[job])
        executable = SimpleNamespace(
            jobset=jobset,
            HasField=lambda f: f == "jobset",
        )
        return SimpleNamespace(
            name=f"experiments/{xid}/workUnits/{index}",
            executable=executable,
        )

    def test_validate_work_units_success_multi_unit(self) -> None:
        stub = mock.MagicMock()
        work_units = [self._create_mock_work_unit("16", i) for i in range(1, 4)]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=work_units)

        results = xmc_entities_validator.validate_work_units(
            stub=stub,
            xid="16",
            expected_count=3,
            expected_worker_names=["demo-worker-1", "demo-worker-2", "demo-worker-3"],
        )
        self.assertEqual(len(results), 3)
        self.assertEqual(
            [r["worker_name"] for r in results],
            ["demo-worker-1", "demo-worker-2", "demo-worker-3"],
        )

    def test_validate_work_units_missing_workers(self) -> None:
        stub = mock.MagicMock()
        wu = self._create_mock_work_unit("16", 1, "demo-worker-1")
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        with self.assertRaises(AssertionError) as ctx:
            xmc_entities_validator.validate_work_units(
                stub=stub,
                xid="16",
                expected_count=1,
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

    def test_make_req_duck_typing(self) -> None:
        req = xmc_entities_validator._make_req("NonExistentRequestClass", foo="bar", answer=42)
        self.assertIsInstance(req, SimpleNamespace)
        self.assertEqual(req.foo, "bar")
        self.assertEqual(req.answer, 42)


if __name__ == "__main__":
    unittest.main()
