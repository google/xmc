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

"""Unit tests for all example verification CLI scripts and common utilities."""

from __future__ import annotations

import io
import os
import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import (
    chart_artifact_job_verify,
    clickhouse_metrics_job_verify,
    common,
    demo_workload_verify,
    init_container_job_verify,
    managed_xprof_artifact_job_verify,
    metrics_dashboard_job_verify,
    metrics_job_verify,
    multipod_job_verify,
    pathways_job_verify,
    per_job_node_selector_verify,
    sidecar_verify,
    simple_job_verify,
    xdash_dashboard_verify,
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

    def test_resolve_dashboard_endpoint(self) -> None:
        self.assertEqual(
            common.resolve_dashboard_endpoint("http://dash.com"),
            "http://dash.com",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("https://dash.com"),
            "https://dash.com",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("localhost:3000"),
            "http://localhost:3000",
        )
        self.assertEqual(
            common.resolve_dashboard_endpoint("api.domain.internal"),
            "https://api.domain.internal",
        )

        with mock.patch.dict(os.environ, {"XMC_DASHBOARD_ENDPOINT": "http://env-dash:9000"}):
            self.assertEqual(
                common.resolve_dashboard_endpoint(None),
                "http://env-dash:9000",
            )

        with mock.patch.dict(
            os.environ,
            {"XMANAGER_UI_URL": "http://xmc-ui:8080"},
            clear=True,
        ):
            self.assertEqual(
                common.resolve_dashboard_endpoint(None),
                "http://xmc-ui:8080",
            )

        with mock.patch.dict(
            os.environ,
            {"XMC_DOMAIN": "xmc.google.internal"},
            clear=True,
        ):
            self.assertEqual(
                common.resolve_dashboard_endpoint(None),
                "https://xmc.google.internal",
            )

        with mock.patch.dict(
            os.environ,
            {"XMANAGER_ENDPOINT": "grpc.api.example.com:443"},
            clear=True,
        ):
            self.assertEqual(
                common.resolve_dashboard_endpoint(None),
                "https://example.com",
            )

    def test_resolve_ui_base_url(self) -> None:
        with mock.patch.object(
            xmc_entities_validator,
            "resolve_ui_base_url",
            return_value="http://localhost:8080",
        ):
            self.assertEqual(common.resolve_ui_base_url(), "http://localhost:8080")

    def test_ess_channel_context(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()
        with mock.patch.object(
            xmc_entities_validator,
            "create_authenticated_stub",
            return_value=(mock_stub, mock_channel),
        ):
            with common.ess_channel_context("localhost:8080") as (
                stub,
                channel,
            ):
                self.assertEqual(stub, mock_stub)
                self.assertEqual(channel, mock_channel)
            mock_channel.close.assert_called_once()

    def test_handle_verification_errors_assertion(self) -> None:
        with self.assertRaises(SystemExit) as cm:
            with common.handle_verification_errors():
                raise AssertionError("Assertion failed test")
        self.assertEqual(cm.exception.code, 1)

    def test_handle_verification_errors_unexpected(self) -> None:
        with self.assertRaises(SystemExit) as cm:
            with common.handle_verification_errors():
                raise RuntimeError("Unexpected failure test")
        self.assertEqual(cm.exception.code, 2)

    def test_print_banner_and_summary(self) -> None:
        buf = io.StringIO()
        with mock.patch("sys.stdout", buf):
            common.print_banner("Test Banner", {"Key": "Value", "List": [1, 2]})
            common.print_success_summary("Summary", [("Status", "Success")])
        out = buf.getvalue()
        self.assertIn("Test Banner", out)
        self.assertIn("Key", out)
        self.assertIn("Summary", out)
        self.assertIn("Status", out)

    def test_run_main(self) -> None:
        called = []

        def dummy_main(argv: list[str]) -> None:
            called.append(True)

        if common.app is not None:
            with mock.patch.object(common.app, "run") as mock_app_run:
                common.run_main(dummy_main)
                mock_app_run.assert_called_once_with(dummy_main)


class ExamplesVerifyTest(unittest.TestCase):
    """Hermetic unit tests executing each verify module's main() with mocked ESS stub."""

    def _mock_stub_and_experiment(self, exp_title: str) -> tuple[mock.MagicMock, mock.MagicMock]:
        stub = mock.MagicMock()
        channel = mock.MagicMock()
        stub.GetExperiment.return_value = SimpleNamespace(
            name="experiments/100",
            title=exp_title,
        )
        return stub, channel

    def _mock_work_unit(
        self,
        worker_name: str = "worker",
        queue: str = "cpu-queue",
        jobs: list[SimpleNamespace] | None = None,
    ) -> SimpleNamespace:
        if jobs is None:
            jobs = [
                SimpleNamespace(
                    name=worker_name,
                    replicas=1,
                    spec=SimpleNamespace(name=worker_name),
                )
            ]
        jobset = SimpleNamespace(jobs=jobs)
        executable = SimpleNamespace(
            jobset=jobset,
            scheduling_constraints=SimpleNamespace(queue_name=queue),
            HasField=lambda f: f in ("jobset", "scheduling_constraints"),
        )
        return SimpleNamespace(
            name="experiments/100/workUnits/1",
            executable=executable,
            current_work_unit_execution=SimpleNamespace(
                status=SimpleNamespace(state=xmc_entities_validator.STATE_COMPLETED),
                HasField=lambda f: f == "status",
            ),
            HasField=lambda f: f == "current_work_unit_execution",
        )

    def test_demo_workload_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Demo Workload (3 units, 60s execution)")
        wus = [self._mock_work_unit(f"demo-worker-{i}", "cpu-queue") for i in range(1, 4)]
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=wus)

        flags_mock = SimpleNamespace(xid="100")
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(demo_workload_verify, "FLAGS", flags_mock),
        ):
            demo_workload_verify.main(["demo_workload_verify"])
            channel.close.assert_called_once()

    def test_simple_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Demo Workload (3 units, 60s execution)")
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

    def test_init_container_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Init Container Validation Experiment")
        job = SimpleNamespace(
            name="init-worker",
            replicas=1,
            init_containers=[SimpleNamespace(name="init-setup", docker_image="")],
            sidecars=[],
            spec=SimpleNamespace(name="primary-worker"),
        )
        wu = self._mock_work_unit("init-worker", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(init_container_job_verify, "FLAGS", flags_mock),
        ):
            init_container_job_verify.main(["init_container_job_verify"])
            channel.close.assert_called_once()

    def test_sidecar_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment(
            "Sidecar & Init Container Validation Experiment"
        )
        job = SimpleNamespace(
            name="sidecar-worker",
            replicas=1,
            init_containers=[SimpleNamespace(name="init-setup", docker_image="")],
            sidecars=[SimpleNamespace(name="sidecar-service", docker_image="")],
            spec=SimpleNamespace(name="primary-worker"),
        )
        wu = self._mock_work_unit("sidecar-worker", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(sidecar_verify, "FLAGS", flags_mock),
        ):
            sidecar_verify.main(["sidecar_verify"])
            channel.close.assert_called_once()

    def test_multipod_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Multi-Pod & Sidecar Validation Experiment")
        job1 = SimpleNamespace(
            name="counter-workers",
            replicas=2,
            init_containers=[],
            sidecars=[SimpleNamespace(name="sidecar-counter", docker_image="")],
            spec=SimpleNamespace(name="primary-counter"),
        )
        job2 = SimpleNamespace(
            name="secondary-counter",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="secondary-counter"),
        )
        wu = self._mock_work_unit("counter-workers", "cpu-queue", [job1, job2])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(multipod_job_verify, "FLAGS", flags_mock),
        ):
            multipod_job_verify.main(["multipod_job_verify"])
            channel.close.assert_called_once()

    def test_per_job_node_selector_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Per-Job nodeSelector Validation Experiment")
        job1 = SimpleNamespace(
            name="head",
            replicas=1,
            node_selector={"cloud.google.com/gke-nodepool": "cpups"},
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="coordinator"),
        )
        job2 = SimpleNamespace(
            name="worker",
            replicas=2,
            node_selector={"cloud.google.com/gke-nodepool": "gpu-pool"},
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="worker-processor"),
        )
        wu = self._mock_work_unit("head", "cpu-queue", [job1, job2])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(per_job_node_selector_verify, "FLAGS", flags_mock),
        ):
            per_job_node_selector_verify.main(["per_job_node_selector_verify"])
            channel.close.assert_called_once()

    def test_chart_artifact_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Chart Artifact Experiment")
        job = SimpleNamespace(
            name="chart-worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="chart-worker"),
        )
        wu = self._mock_work_unit("chart-worker", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        art1 = SimpleNamespace(
            name="artifacts/1",
            parent="experiments/100/workUnits/1",
            payload=SimpleNamespace(
                chart=SimpleNamespace(
                    title="Randomized Training Run",
                    metric_names=["accuracy", "learning_rate", "loss"],
                    status=2,
                    error_message="",
                ),
                WhichOneof=lambda f: "chart",
            ),
        )
        art2 = SimpleNamespace(
            name="artifacts/2",
            parent="experiments/100/workUnits/1",
            payload=SimpleNamespace(
                url=SimpleNamespace(url="https://xmc/dashboards/1", display_name="Metric plots"),
                WhichOneof=lambda f: "url",
            ),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art1, art2])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(chart_artifact_job_verify, "FLAGS", flags_mock),
        ):
            chart_artifact_job_verify.main(["chart_artifact_job_verify"])
            channel.close.assert_called_once()

    def test_clickhouse_metrics_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("ClickHouse Metrics Ingestion Experiment")
        job = SimpleNamespace(
            name="clickhouse-metric-writer",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="clickhouse-metric-writer"),
        )
        wu = self._mock_work_unit("clickhouse-metric-writer", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            dashboard_endpoint=None,
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(clickhouse_metrics_job_verify, "FLAGS", flags_mock),
        ):
            clickhouse_metrics_job_verify.main(["clickhouse_metrics_job_verify"])
            channel.close.assert_called_once()

    def test_metrics_dashboard_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Metrics Dashboard End-to-End Experiment")
        job = SimpleNamespace(
            name="metrics-worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="metrics-worker"),
        )
        wu = self._mock_work_unit("metrics-worker", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            dashboard_endpoint=None,
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(metrics_dashboard_job_verify, "FLAGS", flags_mock),
        ):
            metrics_dashboard_job_verify.main(["metrics_dashboard_job_verify"])
            channel.close.assert_called_once()

    def test_metrics_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Metrics System Validation Experiment")
        jobs = [
            SimpleNamespace(
                name="metrics-manual",
                replicas=1,
                init_containers=[],
                sidecars=[],
                spec=SimpleNamespace(name="metrics-manual"),
            ),
            SimpleNamespace(
                name="metrics-keras",
                replicas=1,
                init_containers=[],
                sidecars=[],
                spec=SimpleNamespace(name="metrics-keras"),
            ),
            SimpleNamespace(
                name="metrics-keras-cb",
                replicas=1,
                init_containers=[],
                sidecars=[],
                spec=SimpleNamespace(name="metrics-keras-cb"),
            ),
            SimpleNamespace(
                name="metrics-lightning",
                replicas=1,
                init_containers=[],
                sidecars=[],
                spec=SimpleNamespace(name="metrics-lightning"),
            ),
        ]
        wu = self._mock_work_unit("metrics-manual", "cpu-queue", jobs)
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(metrics_job_verify, "FLAGS", flags_mock),
        ):
            metrics_job_verify.main(["metrics_job_verify"])
            channel.close.assert_called_once()

    def test_managed_xprof_artifact_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("ManagedXprof WorkUnit Artifact Example")
        job = SimpleNamespace(
            name="programmatic-xprof-worker",
            replicas=1,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="programmatic-xprof-worker"),
        )
        wu = self._mock_work_unit("programmatic-xprof-worker", "cpu-queue", [job])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        art = SimpleNamespace(
            name="artifacts/xprof_1",
            parent="experiments/100/workUnits/1",
            payload=SimpleNamespace(
                managed_xprof=SimpleNamespace(display_name="run_1", run_group="group_1"),
                WhichOneof=lambda f: "managed_xprof",
            ),
        )
        stub.ListArtifacts.return_value = SimpleNamespace(artifacts=[art])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="cpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(managed_xprof_artifact_job_verify, "FLAGS", flags_mock),
        ):
            managed_xprof_artifact_job_verify.main(["managed_xprof_artifact_job_verify"])
            channel.close.assert_called_once()

    def test_pathways_job_verify_main(self) -> None:
        stub, channel = self._mock_stub_and_experiment("Pathways Multi-Component JobSet Validation")
        job1 = SimpleNamespace(
            name="pathways-head",
            replicas=1,
            init_containers=[
                SimpleNamespace(name="pathways-rm", docker_image=""),
                SimpleNamespace(name="pathways-proxy", docker_image=""),
            ],
            sidecars=[],
            spec=SimpleNamespace(name="jax-tpu"),
        )
        job2 = SimpleNamespace(
            name="worker",
            replicas=2,
            init_containers=[],
            sidecars=[],
            spec=SimpleNamespace(name="pathways-worker"),
        )
        wu = self._mock_work_unit("pathways-head", "tpu-queue", [job1, job2])
        stub.ListWorkUnits.return_value = SimpleNamespace(work_units=[wu])

        flags_mock = SimpleNamespace(
            xid="100",
            endpoint="localhost:8080",
            expected_queue="tpu-queue",
            timeout_sec=300,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "create_authenticated_stub",
                return_value=(stub, channel),
            ),
            mock.patch.object(pathways_job_verify, "FLAGS", flags_mock),
        ):
            pathways_job_verify.main(["pathways_job_verify"])
            channel.close.assert_called_once()

    def test_xdash_dashboard_verify_main(self) -> None:
        mock_dash_result = [
            {
                "dashboard_id": "example_vcc_comparison",
                "name": "dashboards/example_vcc_comparison",
                "title": "VCC Baseline vs Tuned Hyperparameters",
                "chart_ids": ["chart_1"],
                "charts": [{"name": "charts/chart_1", "title": "Loss vs Steps"}],
            }
        ]

        flags_mock = SimpleNamespace(
            dashboard_id="example_vcc_comparison",
            xid="dashboard:example_vcc_comparison",
            endpoint="localhost:8080",
            expected_title_pattern="VCC",
            timeout_sec=60,
        )
        with (
            mock.patch.object(
                xmc_entities_validator,
                "validate_dashboard",
                return_value=mock_dash_result,
            ),
            mock.patch.object(xdash_dashboard_verify, "FLAGS", flags_mock),
        ):
            xdash_dashboard_verify.main(["xdash_dashboard_verify"])


if __name__ == "__main__":
    unittest.main()
