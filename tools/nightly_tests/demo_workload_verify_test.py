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

"""Unit tests for demo_workload_verify module."""

from __future__ import annotations

import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import demo_workload_verify, xmc_entities_validator


class DemoWorkloadVerifyTest(unittest.TestCase):
    """Test suite for demo_workload_verify validation script."""

    def test_generate_expected_worker_names_single(self) -> None:
        self.assertEqual(
            demo_workload_verify.generate_expected_worker_names(1),
            ("demo-worker",),
        )
        self.assertEqual(
            demo_workload_verify.generate_expected_worker_names(0),
            ("demo-worker",),
        )

    def test_generate_expected_worker_names_multiple(self) -> None:
        self.assertEqual(
            demo_workload_verify.generate_expected_worker_names(3),
            ("demo-worker-1", "demo-worker-2", "demo-worker-3"),
        )
        self.assertEqual(
            demo_workload_verify.generate_expected_worker_names(4),
            (
                "demo-worker-1",
                "demo-worker-2",
                "demo-worker-3",
                "demo-worker-4",
            ),
        )

    @mock.patch(
        "tools.nightly_tests.common.resolve_ui_base_url", return_value="https://xmc.example.com"
    )
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_experiment")
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_work_units")
    @mock.patch("tools.nightly_tests.common.ess_channel_context")
    def test_main_success_default_flow(
        self,
        mock_channel_ctx: mock.MagicMock,
        mock_validate_wu: mock.MagicMock,
        mock_validate_exp: mock.MagicMock,
        mock_ui_url: mock.MagicMock,
    ) -> None:
        del mock_ui_url
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_channel_ctx.return_value.__enter__.return_value = (mock_stub, mock_channel)

        mock_exp = SimpleNamespace(
            name="experiments/16",
            title="Demo Workload (3 units, 60s execution)",
            creator="test@google.com",
        )
        mock_validate_exp.return_value = mock_exp

        mock_wu_results = [
            {
                "id": f"wu-{i}",
                "name": f"experiments/16/workUnits/{i}",
                "worker_name": f"demo-worker-{i}",
                "queue": "cpu-queue",
                "state_enum": xmc_entities_validator.STATE_COMPLETED,
                "state": "COMPLETED",
            }
            for i in range(1, 4)
        ]
        mock_validate_wu.return_value = mock_wu_results

        flags_obj = SimpleNamespace(
            xid="16",
            endpoint=None,
            timeout=600,
            poll_interval=5,
            expected_work_units=3,
            expected_queue="cpu-queue",
            expected_title_pattern="Demo Workload",
        )

        with mock.patch.object(demo_workload_verify, "FLAGS", flags_obj):
            demo_workload_verify.main(["demo_workload_verify.py"])

        self.assertEqual(mock_validate_exp.call_count, 2)
        mock_validate_exp.assert_called_with(
            stub=mock_stub,
            xid="16",
            expected_title_pattern="Demo Workload",
        )

        mock_validate_wu.assert_called_once_with(
            stub=mock_stub,
            xid="16",
            expected_count=3,
            expected_queue="cpu-queue",
            expected_worker_names=("demo-worker-1", "demo-worker-2", "demo-worker-3"),
            timeout_sec=600,
            poll_interval_sec=5,
            wait_for_completion=True,
        )

    @mock.patch(
        "tools.nightly_tests.common.resolve_ui_base_url", return_value="https://xmc.example.com"
    )
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_experiment")
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_work_units")
    @mock.patch("tools.nightly_tests.common.ess_channel_context")
    def test_main_custom_single_worker_flags(
        self,
        mock_channel_ctx: mock.MagicMock,
        mock_validate_wu: mock.MagicMock,
        mock_validate_exp: mock.MagicMock,
        mock_ui_url: mock.MagicMock,
    ) -> None:
        del mock_ui_url
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_channel_ctx.return_value.__enter__.return_value = (mock_stub, mock_channel)

        mock_exp = SimpleNamespace(
            name="experiments/99",
            title="Custom Workload Run",
            creator="tester@google.com",
        )
        mock_validate_exp.return_value = mock_exp

        mock_wu_results = [
            {
                "id": "wu-1",
                "name": "experiments/99/workUnits/1",
                "worker_name": "demo-worker",
                "queue": "gpu-queue",
                "state_enum": xmc_entities_validator.STATE_COMPLETED,
                "state": "COMPLETED",
            }
        ]
        mock_validate_wu.return_value = mock_wu_results

        flags_obj = SimpleNamespace(
            xid="99",
            endpoint="grpc.test.com:443",
            timeout=180,
            poll_interval=2,
            expected_work_units=1,
            expected_queue="gpu-queue",
            expected_title_pattern="Custom Workload",
        )

        with mock.patch.object(demo_workload_verify, "FLAGS", flags_obj):
            demo_workload_verify.main(["demo_workload_verify.py"])

        mock_validate_exp.assert_called_with(
            stub=mock_stub,
            xid="99",
            expected_title_pattern="Custom Workload",
        )
        mock_validate_wu.assert_called_once_with(
            stub=mock_stub,
            xid="99",
            expected_count=1,
            expected_queue="gpu-queue",
            expected_worker_names=("demo-worker",),
            timeout_sec=180,
            poll_interval_sec=2,
            wait_for_completion=True,
        )

    @mock.patch(
        "tools.nightly_tests.common.resolve_ui_base_url", return_value="https://xmc.example.com"
    )
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_experiment")
    @mock.patch("tools.nightly_tests.xmc_entities_validator.validate_work_units")
    @mock.patch("tools.nightly_tests.common.ess_channel_context")
    def test_main_incomplete_work_unit_state_raises_assertion_error(
        self,
        mock_channel_ctx: mock.MagicMock,
        mock_validate_wu: mock.MagicMock,
        mock_validate_exp: mock.MagicMock,
        mock_ui_url: mock.MagicMock,
    ) -> None:
        del mock_ui_url
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        mock_channel_ctx.return_value.__enter__.return_value = (mock_stub, mock_channel)

        mock_exp = SimpleNamespace(
            name="experiments/16",
            title="Demo Workload",
            creator="test@google.com",
        )
        mock_validate_exp.return_value = mock_exp

        # Return a work unit in RUNNING state instead of COMPLETED
        mock_wu_results = [
            {
                "id": "1",
                "name": "experiments/16/workUnits/1",
                "worker_name": "demo-worker-1",
                "queue": "cpu-queue",
                "state_enum": xmc_entities_validator.STATE_RUNNING,
                "state": "RUNNING",
            }
        ]
        mock_validate_wu.return_value = mock_wu_results

        flags_obj = SimpleNamespace(
            xid="16",
            endpoint=None,
            timeout=600,
            poll_interval=5,
            expected_work_units=1,
            expected_queue="cpu-queue",
            expected_title_pattern="Demo Workload",
        )

        with mock.patch.object(demo_workload_verify, "FLAGS", flags_obj):
            with self.assertRaises(SystemExit) as ctx:
                demo_workload_verify.main(["demo_workload_verify.py"])
            self.assertEqual(ctx.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
