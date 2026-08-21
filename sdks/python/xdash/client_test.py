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
"""Unit tests for xdash DashboardClient using mock gRPC stubs."""

import unittest
from unittest.mock import MagicMock

from xdash._protos import dashboard_service_pb2, messages_pb2
from xdash.client import DashboardClient, XDashRPCError


class MockRpcError(Exception):
    """Custom exception simulating grpc.RpcError in unit tests."""

    is_rpc_error = True

    def code(self) -> str:
        return "NOT_FOUND"

    def details(self) -> str:
        return "Resource not found"


class DashboardClientTest(unittest.TestCase):
    """Tests for DashboardClient read RPC method calls."""

    def setUp(self) -> None:
        self.mock_stub = MagicMock()
        self.client = DashboardClient(stub=self.mock_stub)

    def test_get_dashboard(self) -> None:
        expected = messages_pb2.Dashboard(name="dashboards/123", title="Test Dash")
        self.mock_stub.GetDashboard.return_value = expected

        result = self.client.get_dashboard("dashboards/123")
        self.assertEqual(result.title, "Test Dash")
        self.mock_stub.GetDashboard.assert_called_once()
        req = self.mock_stub.GetDashboard.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.GetDashboardRequest)
        self.assertEqual(req.name, "dashboards/123")

    def test_list_dashboards(self) -> None:
        dash = messages_pb2.Dashboard(name="dashboards/1", title="D1")
        resp = dashboard_service_pb2.ListDashboardsResponse(dashboards=[dash])
        self.mock_stub.ListDashboards.return_value = resp

        results = self.client.list_dashboards()
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0].title, "D1")

    def test_get_chart(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/charts/c1", title="Exp Chart", xid=1001)
        self.mock_stub.GetChart.return_value = chart

        result = self.client.get_chart("experiments/1001/charts/c1")
        self.assertEqual(result.title, "Exp Chart")
        self.mock_stub.GetChart.assert_called_once()

    def test_validation_errors(self) -> None:
        with self.assertRaises(ValueError):
            self.client.get_dashboard("")
        with self.assertRaises(ValueError):
            self.client.get_chart("")

    def test_rpc_error_handling(self) -> None:
        self.mock_stub.GetDashboard.side_effect = MockRpcError()
        with self.assertRaises(XDashRPCError) as cm:
            self.client.get_dashboard("dashboards/invalid")
        self.assertEqual(cm.exception.code, "NOT_FOUND")

    def test_context_manager_closes_channel(self) -> None:
        mock_channel = MagicMock()
        with DashboardClient(stub=self.mock_stub, channel=mock_channel) as client:
            self.assertIs(client._channel, mock_channel)
        mock_channel.close.assert_called_once()

    def test_close_without_channel_is_noop(self) -> None:
        self.client.close()


if __name__ == "__main__":
    unittest.main()
