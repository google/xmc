# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for profile_preparer."""

import shutil
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.components.run_linter import LintResult
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
        "containers": [container],
        "volumes": [{"name": "kafka-tls-credentials", "secret": {"secretName": "rp-tls-sec"}}],
    }
    pod_spec.update(overrides)
    return {"spec": {"replicas": 2, "template": {"spec": pod_spec}}}


class TestProfilePreparer(unittest.TestCase):
    """Tests for legacy-to-components profile preparation and fail-fast audits."""

    def test_audit_live_gateway_deployment_accepts_standard_and_rejects_unmapped(self) -> None:
        flags = profile_preparer.audit_live_gateway_deployment(_baseline_deployment())
        self.assertEqual(flags["tls_ca_file"], "/etc/kafka/tls/ca.crt")

        bad_cases = [
            _baseline_deployment(initContainers=[{"name": "init"}]),
            _baseline_deployment(containers=[{"name": "ingestion-gateway"}, {"name": "sidecar"}]),
            _baseline_deployment(
                container_patch={"env": [{"name": "MESSAGE_BUS", "value": "pubsub"}]}
            ),
            _baseline_deployment(volumes=[{"name": "extra-vol"}]),
            _baseline_deployment(container_patch={"args": ["-message_bus=pubsub"]}),
        ]
        for bad_dep in bad_cases:
            with self.assertRaises(HandledError):
                profile_preparer.audit_live_gateway_deployment(bad_dep)

    def test_build_gateway_redpanda_and_clickhouse_parameters(self) -> None:
        dep = _baseline_deployment()
        flags = profile_preparer.audit_live_gateway_deployment(dep)
        umbrella = {
            "ingestion_gateway_custom_image": "us-docker.pkg.dev/YOUR_GCP_PROJECT_ID/xmc/ig:latest",
            "ingestionGatewayDeployment": {
                "enabled": "true",
                "minReplicas": 1,
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
        self.assertEqual(gw["projectId"], "proj")
        self.assertEqual(gw["minReplicas"], 3)
        self.assertEqual(gw["serviceAccountName"], "ig-sa")
        self.assertEqual(gw["hpaMinReplicas"], 3)
        self.assertEqual(gw["hpaMaxReplicas"], 12)
        self.assertEqual(gw["saslMechanism"], "SCRAM-SHA-512")
        self.assertEqual(gw["tlsSecretName"], "rp-tls-sec")
        self.assertEqual(gw["tlsCaFile"], "/etc/kafka/tls/ca.crt")
        self.assertEqual(gw["cpuRequest"], "25m")
        self.assertEqual(gw["memoryRequest"], "64Mi")
        self.assertEqual(gw["memoryLimit"], "256Mi")

        flags_tls_off = dict(flags, tls_enabled="false")
        gw_no_hpa = profile_preparer.build_gateway_parameters(
            {"ingestionGatewayDeployment": {"replicas": None}},
            {"spec": {"replicas": 0, "template": dep["spec"]["template"]}},
            None,
            flags_tls_off,
        )
        self.assertFalse(gw_no_hpa["hpaEnabled"])
        self.assertEqual(gw_no_hpa["replicas"], 0)
        self.assertFalse(gw_no_hpa["tlsEnabled"])
        self.assertNotIn("tlsSecretName", gw_no_hpa)
        self.assertNotIn("tlsCaFile", gw_no_hpa)

        bad_dep = _baseline_deployment(container_patch={"image": ""})
        with self.assertRaises(HandledError):
            profile_preparer.build_gateway_parameters(umbrella, bad_dep, None, flags)

        rp = profile_preparer.build_redpanda_parameters(gw)
        self.assertEqual(rp["brokerEndpoints"], "rp-0:9092,rp-1:9092")
        ch = profile_preparer.build_clickhouse_parameters(umbrella)
        self.assertEqual(ch["clusterEndpoints"], "[fd00::1]:9000")
        self.assertEqual(ch["healthCheckEndpoint"], "http://[fd00::1]:8123/ping")
        self.assertEqual(ch["user"], "writer")

    def test_render_profile_yaml_types_and_guards(self) -> None:
        rendered = profile_preparer.render_profile_yaml({"empty": {}, "ratio": 0.5, "flag": False})
        self.assertIn("empty: {}", rendered)
        self.assertIn("ratio: 0.5", rendered)
        self.assertIn("flag: false", rendered)
        with self.assertRaises(ValueError):
            profile_preparer.render_profile_yaml({"bad": None})

    @patch("tools.migrations.components.profile_preparer.validate_profile_values")
    @patch("tools.migrations.components.profile_preparer.verify_secret_exists")
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
        mock_secret: MagicMock,
        mock_validate: MagicMock,
    ) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "pv.yaml"
            with self.assertRaises(HandledError):
                profile_preparer.prepare_profile_values("xmc", "INVALID_NS", out)

            mock_umbrella.return_value = {"ingestionGatewayDeployment": {"enabled": "false"}}
            res_off = profile_preparer.prepare_profile_values(
                "xmc", "tenant-a", out, Path(tmp), Path(tmp)
            )
            self.assertFalse(res_off["gateway_enabled"])
            self.assertIn("enabled: false", out.read_text(encoding="utf-8"))
            mock_live.assert_not_called()

            mock_umbrella.return_value = {
                "ingestion_gateway_custom_image": "gcr.io/p/ig:1.0",
                "ingestionGatewayDeployment": {"enabled": True},
                "secrets": {"csi": {"enabled": "false"}},
            }
            mock_live.side_effect = [
                _baseline_deployment(),
                {"spec": {"minReplicas": 3, "maxReplicas": 10}},
            ]
            res_on = profile_preparer.prepare_profile_values(
                "xmc", "tenant-a", out, Path(tmp), Path(tmp)
            )
            self.assertTrue(res_on["gateway_enabled"])
            self.assertEqual(mock_secret.call_count, 2)  # TLS + ClickHouse
            self.assertEqual(mock_validate.call_count, 2)

            mock_helm_ver.side_effect = RuntimeError("helm v3.12.0 < 3.14.0")
            with self.assertRaises(HandledError):
                profile_preparer.prepare_profile_values("xmc", "tenant-a", out)

    @patch("tools.migrations.components.profile_preparer.run_linter_on_target")
    def test_validate_profile_values_against_real_schema_if_helm_installed(
        self, mock_linter: MagicMock
    ) -> None:
        if shutil.which("helm") is None:
            self.skipTest("helm binary not installed")
        profile_dir = next(
            (
                p / "deploy" / "profiles" / "stable-2026a"
                for p in (Path.cwd(), *Path.cwd().parents)
                if (p / "deploy" / "profiles" / "stable-2026a" / "Chart.yaml").is_file()
            ),
            None,
        )
        if profile_dir is None:
            self.skipTest("stable-2026a profile chart not in test runfiles")
        seen_manifests: list[str] = []

        def _check_lint(target: Any, *_args: Any, **_kwargs: Any) -> LintResult:
            seen_manifests.extend(
                mf.read_text(encoding="utf-8") for mf in Path(target.manifests_dir).glob("*.yaml")
            )
            return LintResult(
                manifests_dir=str(target.manifests_dir),
                rules_config=None,
                success=True,
                returncode=0,
                stdout="{}",
                stderr="",
            )

        mock_linter.side_effect = _check_lint
        with tempfile.TemporaryDirectory() as tmp:
            pv = Path(tmp) / "profile-values.yaml"
            doc = {
                "components": {
                    "redpanda": {
                        "enabled": True,
                        "namespace": "tenant-a",
                        "parameters": {
                            "brokerEndpoints": "rp-0:9092",
                            "kafkaPort": 9092,
                            "defaultTopic": "t",
                        },
                    },
                    "clickhouse": {
                        "enabled": True,
                        "namespace": "tenant-a",
                        "parameters": {
                            "host": "ch.svc",
                            "nativePort": 9000,
                            "secretName": "clickhouse-secret",
                        },
                    },
                    "ingestion-gateway": {
                        "enabled": True,
                        "namespace": "tenant-a",
                        "parameters": {
                            "image": "gcr.io/p/ig:1.0",
                            "hpaEnabled": True,
                            "hpaMinReplicas": 3,
                        },
                    },
                }
            }
            pv.write_text(profile_preparer.render_profile_yaml(doc), encoding="utf-8")
            profile_preparer.validate_profile_values(
                pv, "tenant-a", profile_dir, profile_dir.parents[2]
            )
            self.assertEqual(len(seen_manifests), 1)
            self.assertIn('namespace: "tenant-a"', seen_manifests[0])


if __name__ == "__main__":
    unittest.main()
