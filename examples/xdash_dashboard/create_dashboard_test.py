# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for examples/xdash_dashboard/create_dashboard.py."""

from __future__ import annotations

import contextlib
import sys
import types
import unittest
from unittest import mock

import create_dashboard


class CreateDashboardTest(unittest.TestCase):
    """Tests explicit experiment creation flag and fail-fast import behavior."""

    def test_resolve_experiment_runs_defaults_to_fixed_runs(self) -> None:
        runs = create_dashboard._resolve_experiment_runs()
        self.assertEqual(runs, list(create_dashboard.EXPERIMENT_RUNS))

    def test_resolve_experiment_runs_false_returns_fixed_runs(self) -> None:
        runs = create_dashboard._resolve_experiment_runs(create_experiments=False)
        self.assertEqual(runs, list(create_dashboard.EXPERIMENT_RUNS))

    def test_resolve_experiment_runs_true_raises_import_error_when_xmanager_missing(self) -> None:
        with mock.patch.dict(
            sys.modules,
            {
                "xmanager": None,
                "xmanager.xm_cloud": None,
                "xmanager_cloud": None,
                "xmanager_cloud.xid_service": None,
                "xmanager_cloud.xid_service.proto": None,
            },
        ):
            with self.assertRaises(ImportError):
                create_dashboard._resolve_experiment_runs(create_experiments=True)

    def test_resolve_experiment_runs_true_creates_live_experiments(self) -> None:
        created_titles: list[str] = []
        next_xids = iter([5001, 5002, 5003])

        @contextlib.contextmanager
        def fake_create_experiment(experiment_title: str, acls: object):
            created_titles.append(experiment_title)
            xid = next(next_xids)
            yield types.SimpleNamespace(resource_name=f"experiments/{xid}")

        fake_xm_cloud = types.ModuleType("xmanager.xm_cloud")
        fake_xm_cloud.create_experiment = fake_create_experiment  # type: ignore[attr-defined]
        fake_snapshot = types.ModuleType("xmanager.xm_cloud.snapshot_code")
        fake_xm_cloud.snapshot_code = fake_snapshot  # type: ignore[attr-defined]

        fake_xmanager = types.ModuleType("xmanager")
        fake_xmanager.xm_cloud = fake_xm_cloud  # type: ignore[attr-defined]

        class FakeXidAcls:
            class Role:
                def __init__(self, all_authenticated_users: bool = False) -> None:
                    self.all_authenticated_users = all_authenticated_users

            def __init__(self, owners: list[object]) -> None:
                self.owners = owners

        fake_messages_pb2 = types.ModuleType("xmanager_cloud.xid_service.proto.messages_pb2")
        fake_messages_pb2.XidAcls = FakeXidAcls  # type: ignore[attr-defined]
        fake_proto_pkg = types.ModuleType("xmanager_cloud.xid_service.proto")
        fake_proto_pkg.messages_pb2 = fake_messages_pb2  # type: ignore[attr-defined]

        with mock.patch.dict(
            sys.modules,
            {
                "xmanager": fake_xmanager,
                "xmanager.xm_cloud": fake_xm_cloud,
                "xmanager.xm_cloud.snapshot_code": fake_snapshot,
                "xmanager_cloud": types.ModuleType("xmanager_cloud"),
                "xmanager_cloud.xid_service": types.ModuleType("xmanager_cloud.xid_service"),
                "xmanager_cloud.xid_service.proto": fake_proto_pkg,
                "xmanager_cloud.xid_service.proto.messages_pb2": fake_messages_pb2,
            },
        ):
            runs = create_dashboard._resolve_experiment_runs(create_experiments=True)

        self.assertEqual(
            runs,
            [
                (5001, "baseline_5001", "VCC Baseline Run"),
                (5002, "tuned_v1_5002", "VCC Tuned Run (lr=0.01)"),
                (5003, "tuned_v2_5003", "VCC Tuned Run (lr=0.005)"),
            ],
        )
        self.assertEqual(
            created_titles,
            [title for _, _, title in create_dashboard.EXPERIMENT_RUNS],
        )

    @mock.patch("create_dashboard.DashboardClient")
    @mock.patch("create_dashboard.create_example_dashboard")
    def test_main_wires_create_experiments_flag(
        self,
        mock_create_example_dashboard: mock.MagicMock,
        mock_client_cls: mock.MagicMock,
    ) -> None:
        mock_client = mock.MagicMock()
        mock_client_cls.return_value.__enter__.return_value = mock_client

        rc_default = create_dashboard.main(["--endpoint", "localhost:50051"])
        self.assertEqual(rc_default, 0)
        mock_create_example_dashboard.assert_called_once_with(
            client=mock_client,
            dashboard_id=create_dashboard.DEFAULT_DASHBOARD_ID,
            title=create_dashboard.DEFAULT_DASHBOARD_TITLE,
            create_experiments=False,
        )

        mock_create_example_dashboard.reset_mock()
        rc_explicit = create_dashboard.main(
            ["--endpoint", "localhost:50051", "--create-experiments"]
        )
        self.assertEqual(rc_explicit, 0)
        mock_create_example_dashboard.assert_called_once_with(
            client=mock_client,
            dashboard_id=create_dashboard.DEFAULT_DASHBOARD_ID,
            title=create_dashboard.DEFAULT_DASHBOARD_TITLE,
            create_experiments=True,
        )


if __name__ == "__main__":
    unittest.main()
