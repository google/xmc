# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for profile_preparer."""

import math
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.migrations.components import profile_preparer


def _baseline_deployment(**overrides: Any) -> dict[str, Any]:
    container: dict[str, Any] = {
        "name": "ingestion-gateway",
        "image": "us-docker.pkg.dev/proj/xmc/ingestion-gateway:v1.2.3",
        "args": [
            "-port=8080",
            "-enable_components=false",
            "-components_namespace=xmc",
            "-redpanda_brokers=rp-0:9092,rp-1:9092",
            "-topic_name=xmc-metrics",
            "-min_partitions=128",
            "-min_replicas=3",
            "-project_id=proj",
            "-tls_enabled=1",
            "-tls_ca_file=/etc/kafka/tls/ca.crt",
        ],
        "resources": {"requests": {"cpu": "25m"}},
    }
    container.update(overrides.pop("container_patch", {}))
    pod_spec: dict[str, Any] = {
        "serviceAccountName": "ig-sa",
        "containers": [{"name": "istio-proxy", "image": "proxy:1"}, container],
        "volumes": [{"name": "kafka-tls-credentials", "secret": {"secretName": "rp-tls-sec"}}],
    }
    pod_spec.update(overrides)
    return {"spec": {"replicas": 2, "template": {"spec": pod_spec}}}


class TestProfilePreparer(unittest.TestCase):
    """Tests for legacy-to-components profile parameter synthesis."""

    def test_build_gateway_redpanda_and_clickhouse_parameters(self) -> None:
        dep = _baseline_deployment()
        flags = profile_preparer.parse_gateway_cli_flags(dep)
        umbrella = {
            "ingestion_gateway_custom_image": "us-docker.pkg.dev/YOUR_GCP_PROJECT_ID/xmc/ig:latest",
            "ingestionGatewayDeployment": {
                "enabled": "true",
                "minReplicas": 1,  # helm get values -a default must NOT shadow live -min_replicas=3
                "resources": {
                    "requests": {"cpu": "10m", "memory": "64Mi"},
                    "limits": {"memory": "256Mi"},
                },
                "sasl": {
                    "enabled": True,
                    "mechanism": "SCRAM-SHA-512",
                    "secretName": "rp-sasl-sec",
                },
            },
            "metrics": {"clickhouse": {"host": "fd00::1", "port": "9000", "user": "writer"}},
        }
        hpa = {"spec": {"minReplicas": 3, "maxReplicas": 12}}
        gw = profile_preparer.build_gateway_parameters(umbrella, dep, hpa, flags)
        self.assertEqual(gw["image"], "us-docker.pkg.dev/proj/xmc/ingestion-gateway:v1.2.3")
        self.assertEqual(gw["minReplicas"], 3)
        self.assertEqual(gw["serviceAccountName"], "ig-sa")
        self.assertEqual(gw["hpaMinReplicas"], 3)
        self.assertEqual(gw["hpaMaxReplicas"], 12)
        self.assertEqual(gw["cpuRequest"], "25m")
        self.assertEqual(gw["memoryRequest"], "64Mi")
        self.assertEqual(gw["memoryLimit"], "256Mi")

        gw_no_hpa = profile_preparer.build_gateway_parameters(
            {"ingestionGatewayDeployment": {"replicas": None}},
            {"spec": {"replicas": 0, "template": dep["spec"]["template"]}},
            None,
            flags,
        )
        self.assertFalse(gw_no_hpa["hpaEnabled"])
        self.assertEqual(gw_no_hpa["replicas"], 0)

        bad_dep = _baseline_deployment(container_patch={"image": ""})
        with self.assertRaises(HandledError):
            profile_preparer.build_gateway_parameters(umbrella, bad_dep, None, flags)

        rp = profile_preparer.build_redpanda_parameters(gw)
        self.assertEqual(rp["brokerEndpoints"], "rp-0:9092,rp-1:9092")
        self.assertEqual(rp["kafkaPort"], 9092)
        ch = profile_preparer.build_clickhouse_parameters(umbrella)
        self.assertEqual(ch["clusterEndpoints"], "[fd00::1]:9000")
        self.assertEqual(ch["user"], "writer")

    def test_parse_gateway_cli_flags_space_separated_bool_and_terminator(self) -> None:
        dep = _baseline_deployment(
            container_patch={
                "args": [
                    "positional-ignored",
                    "-",
                    "-tls_enabled",
                    "-port",
                    "9090",
                    "--topic_name",
                    "custom-metrics",
                    "-project_id",
                    "--min_partitions=64",
                    "--",
                    "-port=9999",
                ]
            }
        )
        parsed = profile_preparer.parse_gateway_cli_flags(dep)
        self.assertEqual(
            parsed,
            {
                "tls_enabled": "true",
                "port": "9090",
                "topic_name": "custom-metrics",
                "min_partitions": "64",
            },
        )

    def test_build_redpanda_parameters_ipv6_and_port_edge_cases(self) -> None:
        cases = [
            ("fd00::1", 9092),
            ("fd00::1,fd00::2", 9092),
            ("[fd00::1]:9094", 9094),
            ("[fd00::1]", 9092),
            ("rp-0", 9092),
            ("rp-0:19092", 19092),
            ("rp-0:0", 9092),
            ("rp-0:70000", 9092),
        ]
        for brokers, expected_port in cases:
            with self.subTest(brokers=brokers):
                rp = profile_preparer.build_redpanda_parameters({"redpandaBrokers": brokers})
                self.assertEqual(rp["kafkaPort"], expected_port)
                self.assertEqual(rp["brokerEndpoints"], brokers)

    def test_non_dict_malformed_helm_and_k8s_mappings_fallback_safely(self) -> None:
        dep_malformed = {
            "spec": {
                "replicas": 4,
                "template": {
                    "spec": {
                        "containers": [
                            "not-a-dict",
                            {
                                "name": "ingestion-gateway",
                                "image": "gcr.io/p/ig:1.0",
                                "args": "not-a-list",
                                "resources": {"requests": "bad", "limits": ["bad"]},
                            },
                        ]
                    }
                },
            }
        }
        self.assertEqual(profile_preparer.parse_gateway_cli_flags(dep_malformed), {})
        gw = profile_preparer.build_gateway_parameters(
            {"ingestionGatewayDeployment": "not-a-dict"},
            dep_malformed,
            {"spec": "not-a-dict"},
            {},
        )
        self.assertEqual(gw["hpaMinReplicas"], 3)
        self.assertEqual(gw["hpaMaxReplicas"], 10)
        gw2 = profile_preparer.build_gateway_parameters(
            {"ingestionGatewayDeployment": {"hpa": "bad", "resources": "bad"}},
            dep_malformed,
            None,
            {},
        )
        self.assertEqual(gw2["replicas"], 4)
        for bad_metrics in ({"metrics": "bad"}, {"metrics": {"clickhouse": ["bad"]}}):
            ch = profile_preparer.build_clickhouse_parameters(bad_metrics)
            self.assertEqual(ch["host"], "clickhouse.xmc-metrics.svc.cluster.local")

    def test_render_profile_yaml_types_and_guards(self) -> None:
        rendered = profile_preparer.render_profile_yaml({"empty": {}, "ratio": 0.5, "flag": False})
        self.assertIn("empty: {}", rendered)
        self.assertIn("ratio: 0.5", rendered)
        self.assertIn("flag: false", rendered)
        with self.assertRaises(ValueError):
            profile_preparer.render_profile_yaml({"bad": None})
        with self.assertRaises(ValueError):
            profile_preparer.render_profile_yaml({"nan": math.nan})

    @patch("tools.migrations.components.profile_preparer.fetch_live_resource")
    @patch("tools.migrations.components.profile_preparer.fetch_umbrella_values")
    @patch(
        "tools.migrations.components.profile_preparer.verify_helm_min_version",
        return_value="v3.15.0",
    )
    def test_prepare_profile_values_disabled_enabled_and_errors(
        self,
        mock_helm_ver: MagicMock,
        mock_umbrella: MagicMock,
        mock_live: MagicMock,
    ) -> None:
        with self.assertRaises(HandledError):
            profile_preparer.build_gateway_parameters(
                {}, _baseline_deployment(), None, {"port": "abc"}
            )
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "pv.yaml"
            for bad_ns in ("INVALID_NS", "tenant-a\n"):
                with self.assertRaises(HandledError):
                    profile_preparer.prepare_profile_values("xmc", bad_ns, out)

            mock_umbrella.return_value = {"ingestionGatewayDeployment": "not-a-dict"}
            self.assertFalse(
                profile_preparer.prepare_profile_values("xmc", "tenant-a", out)["gateway_enabled"]
            )

            mock_umbrella.return_value = {"ingestionGatewayDeployment": {"enabled": "false"}}
            with self.assertRaises(HandledError):
                profile_preparer.prepare_profile_values("xmc", "tenant-a", Path(tmp))

            res_off = profile_preparer.prepare_profile_values("xmc", "tenant-a", out)
            self.assertFalse(res_off["gateway_enabled"])
            self.assertIn("enabled: false", out.read_text(encoding="utf-8"))
            mock_live.assert_not_called()

            mock_umbrella.return_value = {
                "ingestion_gateway_custom_image": "gcr.io/p/ig:1.0",
                "ingestionGatewayDeployment": {"enabled": True},
            }
            mock_live.side_effect = [
                _baseline_deployment(),
                {"spec": {"minReplicas": 3, "maxReplicas": 10}},
            ]
            res_on = profile_preparer.prepare_profile_values("xmc", "tenant-a", out)
            self.assertTrue(res_on["gateway_enabled"])
            self.assertIn('image: "gcr.io/p/ig:1.0"', out.read_text(encoding="utf-8"))

            mock_helm_ver.side_effect = RuntimeError("helm v3.12.0 < 3.14.0")
            with self.assertRaises(HandledError):
                profile_preparer.prepare_profile_values("xmc", "tenant-a", out)


if __name__ == "__main__":
    unittest.main()
