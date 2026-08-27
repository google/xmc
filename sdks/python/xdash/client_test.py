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
from unittest.mock import MagicMock, patch

from xdash._protos import dashboard_service_pb2, messages_pb2
from xdash.builders import DashboardBuilder
from xdash.client import DashboardClient, XDashError, XDashRPCError, _is_local_endpoint


class FakeRpcError(Exception):
    """Stands in for grpc.RpcError, which these targets do not depend on.

    The production code identifies an RPC failure with isinstance(err, grpc.RpcError),
    so tests patch xdash.client.grpc and point its RpcError at this class rather than
    asking the client to recognise a marker attribute no real gRPC error carries.
    """

    def code(self) -> str:
        return "NOT_FOUND"

    def details(self) -> str:
        return "Resource not found"


class DetaillessRpcError(FakeRpcError):
    """An RPC failure whose details() is empty, as a cancelled call's can be."""

    def details(self) -> str:
        return ""


class DashboardClientTest(unittest.TestCase):
    """Tests for DashboardClient RPC method calls."""

    def setUp(self) -> None:
        self.mock_stub = MagicMock()
        self.client = DashboardClient(stub=self.mock_stub)

    def test_create_dashboard(self) -> None:
        dash = DashboardBuilder(name="dashboards/vcc_comparison").set_title("VCC").build()
        self.mock_stub.CreateDashboard.return_value = dash

        result = self.client.create_dashboard(dash, dashboard_id="vcc_comparison")
        self.assertEqual(result.name, dash.name)
        self.mock_stub.CreateDashboard.assert_called_once()
        req = self.mock_stub.CreateDashboard.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.CreateDashboardRequest)
        self.assertEqual(req.dashboard_id, "vcc_comparison")

    def test_create_dashboard_derives_id_from_name(self) -> None:
        dash = DashboardBuilder(name="dashboards/derived").set_title("Derived").build()
        self.mock_stub.CreateDashboard.return_value = dash

        self.client.create_dashboard(dash)
        req = self.mock_stub.CreateDashboard.call_args[0][0]
        self.assertEqual(req.dashboard_id, "derived")

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
        self.mock_stub.ListDashboards.assert_called_once()
        req = self.mock_stub.ListDashboards.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.ListDashboardsRequest)

    def test_list_dashboards_on_empty_response(self) -> None:
        self.mock_stub.ListDashboards.return_value = dashboard_service_pb2.ListDashboardsResponse()
        self.assertEqual(self.client.list_dashboards(), [])

    def test_create_dashboard_derives_the_id_from_the_resource_name(self) -> None:
        dashboard = messages_pb2.Dashboard(name="dashboards/d1", title="D1")
        self.mock_stub.CreateDashboard.return_value = dashboard

        self.client.create_dashboard(dashboard)
        req = self.mock_stub.CreateDashboard.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.CreateDashboardRequest)
        self.assertEqual(req.dashboard_id, "d1")
        self.assertEqual(req.dashboard.title, "D1")

    def test_create_dashboard_prefers_an_explicit_id(self) -> None:
        dashboard = messages_pb2.Dashboard(name="dashboards/d1", title="D1")
        self.mock_stub.CreateDashboard.return_value = dashboard

        self.client.create_dashboard(dashboard, dashboard_id="override")
        self.assertEqual(self.mock_stub.CreateDashboard.call_args[0][0].dashboard_id, "override")

    def test_create_chart_sends_the_parent_chart_and_id(self) -> None:
        chart = messages_pb2.Chart(
            name="experiments/1001/workUnits/1/charts/c1",
            title="C1",
            xid=1001,
            wid=1,
        )
        # A distinct return value: asserting the result equals the input would hold even
        # if create_chart never called the stub at all.
        self.mock_stub.CreateChart.return_value = messages_pb2.Chart(name=chart.name, title="C1")

        result = self.client.create_chart(
            parent="experiments/1001/workUnits/1", chart=chart, chart_id="c1"
        )
        self.assertEqual(result.name, "experiments/1001/workUnits/1/charts/c1")
        self.mock_stub.CreateChart.assert_called_once()
        req = self.mock_stub.CreateChart.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.CreateChartRequest)
        self.assertEqual(req.parent, "experiments/1001/workUnits/1")
        self.assertEqual(req.chart_id, "c1")
        self.assertEqual((req.chart.xid, req.chart.wid), (1001, 1))

    def test_create_chart_derives_the_id_from_the_resource_name(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/workUnits/1/charts/derived", xid=1001)
        self.mock_stub.CreateChart.return_value = chart

        self.client.create_chart(parent="experiments/1001/workUnits/1", chart=chart)
        self.assertEqual(self.mock_stub.CreateChart.call_args[0][0].chart_id, "derived")

    def test_create_chart_ignores_a_trailing_slash_when_deriving_the_id(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/workUnits/1/charts/derived/", xid=1001)
        self.mock_stub.CreateChart.return_value = chart

        self.client.create_chart(parent="experiments/1001/workUnits/1", chart=chart)
        # A trailing slash used to derive "", which the server rejects with an opaque error.
        self.assertEqual(self.mock_stub.CreateChart.call_args[0][0].chart_id, "derived")

    def test_create_charts_derives_each_parent_from_the_chart(self) -> None:
        charts = [
            messages_pb2.Chart(name="experiments/1001/workUnits/1/charts/a", xid=1001, wid=1),
            messages_pb2.Chart(name="experiments/1002/workUnits/7/charts/b", xid=1002, wid=7),
        ]
        self.mock_stub.CreateChart.side_effect = charts

        results = self.client.create_charts(charts)
        self.assertEqual([c.name for c in results], [charts[0].name, charts[1].name])
        parents = [call[0][0].parent for call in self.mock_stub.CreateChart.call_args_list]
        # The wid varies between the two charts, so a hardcoded parent would fail here.
        self.assertEqual(parents, ["experiments/1001/workUnits/1", "experiments/1002/workUnits/7"])

    def test_create_charts_rejects_a_chart_without_an_experiment(self) -> None:
        # Without xid/wid there is no parent to send, and the derived
        # "experiments/0/workUnits/0" would be rejected by the server with an opaque error.
        for bad in (
            messages_pb2.Chart(name="charts/no_xid", wid=1),
            messages_pb2.Chart(name="charts/no_wid", xid=1001),
        ):
            with self.assertRaises(ValueError):
                self.client.create_charts([bad])
        self.mock_stub.CreateChart.assert_not_called()

    def test_create_charts_rejects_a_none_element(self) -> None:
        # A None must fail the same way create_chart's own guard does, rather than as
        # an AttributeError from reading .xid off it.
        with self.assertRaises(ValueError):
            self.client.create_charts([None])  # type: ignore[list-item]

    def test_get_chart(self) -> None:
        chart = messages_pb2.Chart(name="experiments/1001/charts/c1", title="Exp Chart", xid=1001)
        self.mock_stub.GetChart.return_value = chart

        result = self.client.get_chart("experiments/1001/charts/c1")
        self.assertEqual(result.title, "Exp Chart")
        self.mock_stub.GetChart.assert_called_once()
        req = self.mock_stub.GetChart.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.GetChartRequest)
        self.assertEqual(req.name, "experiments/1001/charts/c1")

    def test_get_chart_data(self) -> None:
        self.mock_stub.GetChartData.return_value = messages_pb2.ChartData(
            name="experiments/1001/charts/c1", xid=1001
        )

        result = self.client.get_chart_data("experiments/1001/charts/c1")
        self.assertEqual(result.xid, 1001)
        self.mock_stub.GetChartData.assert_called_once()
        req = self.mock_stub.GetChartData.call_args[0][0]
        self.assertIsInstance(req, dashboard_service_pb2.GetChartDataRequest)
        self.assertEqual(req.name, "experiments/1001/charts/c1")
        self.mock_stub.GetChart.assert_not_called()

    def test_validation_errors(self) -> None:
        with self.assertRaises(ValueError):
            self.client.create_dashboard(None)  # type: ignore[arg-type]
        with self.assertRaises(ValueError):
            self.client.create_chart(parent="experiments/1/workUnits/1", chart=None)  # type: ignore[arg-type]
        for blank in ("", "   ", "\t\n"):
            with self.assertRaises(ValueError):
                self.client.get_dashboard(blank)
            with self.assertRaises(ValueError):
                self.client.get_chart(blank)
            with self.assertRaises(ValueError):
                self.client.get_chart_data(blank)
            with self.assertRaises(ValueError):
                self.client.create_chart(parent=blank, chart=messages_pb2.Chart())
        for stub_method in (
            self.mock_stub.CreateDashboard,
            self.mock_stub.GetDashboard,
            self.mock_stub.CreateChart,
            self.mock_stub.GetChart,
            self.mock_stub.GetChartData,
        ):
            stub_method.assert_not_called()

    def test_rpc_error_handling(self) -> None:
        self.mock_stub.GetDashboard.side_effect = FakeRpcError()
        with patch("xdash.client.grpc") as mock_grpc:
            mock_grpc.RpcError = FakeRpcError
            with self.assertRaises(XDashRPCError) as cm:
                self.client.get_dashboard("dashboards/invalid")
        self.assertEqual(cm.exception.code, "NOT_FOUND")
        self.assertEqual(str(cm.exception), "GetDashboard failed: Resource not found")

    def test_rpc_error_without_details_falls_back_to_the_exception(self) -> None:
        self.mock_stub.GetDashboard.side_effect = DetaillessRpcError("channel closed")
        with patch("xdash.client.grpc") as mock_grpc:
            mock_grpc.RpcError = FakeRpcError
            with self.assertRaises(XDashRPCError) as cm:
                self.client.get_dashboard("dashboards/invalid")
        self.assertEqual(str(cm.exception), "GetDashboard failed: channel closed")

    def test_non_rpc_error_is_wrapped_as_xdash_error(self) -> None:
        self.mock_stub.GetChart.side_effect = RuntimeError("boom")
        with patch("xdash.client.grpc") as mock_grpc:
            mock_grpc.RpcError = FakeRpcError
            with self.assertRaises(XDashError) as cm:
                self.client.get_chart("experiments/1/charts/c1")
        self.assertNotIsInstance(cm.exception, XDashRPCError)
        self.assertEqual(str(cm.exception), "Unexpected error in GetChart: boom")

    def test_injected_channel_is_left_open(self) -> None:
        mock_channel = MagicMock()
        with DashboardClient(stub=self.mock_stub, channel=mock_channel) as client:
            self.assertIs(client._channel, mock_channel)
        # The caller may be sharing this channel with other clients; closing it is not ours to do.
        mock_channel.close.assert_not_called()

    def test_owned_channel_is_closed(self) -> None:
        with patch("xdash.client.grpc") as mock_grpc, patch("xdash.client.DashboardServiceStub"):
            with DashboardClient(endpoint="localhost:8080"):
                pass
        mock_grpc.insecure_channel.return_value.close.assert_called_once()

    def test_close_without_channel_is_noop(self) -> None:
        self.client.close()


class ChannelSecurityTest(unittest.TestCase):
    """Tests for how DashboardClient chooses between plaintext and TLS channels."""

    def test_local_endpoints_are_recognised(self) -> None:
        for endpoint in ("localhost:8080", "127.0.0.1:8080", "[::1]:8080", "0.0.0.0:8080"):
            self.assertTrue(_is_local_endpoint(endpoint), endpoint)
        for endpoint in ("cloud.xmanager.googleapis.com:443", "10.0.0.5:8080"):
            self.assertFalse(_is_local_endpoint(endpoint), endpoint)

    def test_remote_endpoint_defaults_to_tls(self) -> None:
        with patch("xdash.client.grpc") as mock_grpc:
            DashboardClient(endpoint="cloud.xmanager.googleapis.com:443")
        mock_grpc.secure_channel.assert_called_once_with(
            "cloud.xmanager.googleapis.com:443", mock_grpc.ssl_channel_credentials.return_value
        )
        mock_grpc.insecure_channel.assert_not_called()

    def test_local_endpoint_defaults_to_plaintext(self) -> None:
        with patch("xdash.client.grpc") as mock_grpc:
            DashboardClient(endpoint="localhost:8080")
        mock_grpc.insecure_channel.assert_called_once_with("localhost:8080")
        mock_grpc.secure_channel.assert_not_called()

    def test_secure_false_forces_plaintext_for_remote_endpoint(self) -> None:
        with patch("xdash.client.grpc") as mock_grpc:
            DashboardClient(endpoint="dashboards.example.com:443", secure=False)
        mock_grpc.insecure_channel.assert_called_once_with("dashboards.example.com:443")
        mock_grpc.secure_channel.assert_not_called()

    def test_explicit_credentials_are_used_and_imply_tls(self) -> None:
        creds = object()
        with patch("xdash.client.grpc") as mock_grpc:
            DashboardClient(endpoint="localhost:8080", credentials=creds)
        mock_grpc.secure_channel.assert_called_once_with("localhost:8080", creds)
        mock_grpc.ssl_channel_credentials.assert_not_called()

    def test_credentials_with_secure_false_is_rejected(self) -> None:
        with patch("xdash.client.grpc"), self.assertRaises(ValueError):
            DashboardClient(endpoint="localhost:8080", credentials=object(), secure=False)


if __name__ == "__main__":
    unittest.main()
