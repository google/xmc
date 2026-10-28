# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for the one-time XMC component migration CLI."""

from __future__ import annotations

import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.migrations.components import migrate


def _helm_obj(name: str, release: str = "xmc", namespace: str = "xmc") -> dict[str, object]:
    """Builds a minimal Kubernetes manifest dict carrying Helm ownership metadata."""
    return {
        "metadata": {
            "name": name,
            "labels": {"app.kubernetes.io/managed-by": "Helm"},
            "annotations": {
                "meta.helm.sh/release-name": release,
                "meta.helm.sh/release-namespace": namespace,
            },
        }
    }


def _done(
    cmd: list[str], stdout: str = "", stderr: str = "", rc: int = 0
) -> subprocess.CompletedProcess[str]:
    """Builds a CompletedProcess instance for subprocess command mocks."""
    return subprocess.CompletedProcess(cmd, returncode=rc, stdout=stdout, stderr=stderr)


class PrepareTests(unittest.TestCase):
    """Tests for `migrate.py prepare` value mapping and validation."""

    def test_validate_identifiers_rejects_invalid_names_and_newlines(self) -> None:
        for bad_ns in ("", "-bad", "UPPER", "xmc\n", "a" * 64):
            with self.assertRaises(HandledError):
                migrate._validate_identifiers(bad_ns, "xmc")
        for bad_rel in ("", "-xmc", "xmc-cmp-ingestion-gateway", "xmc\n", "a" * 54):
            with self.assertRaises(HandledError):
                migrate._validate_identifiers("xmc", bad_rel)

    def test_prepare_maps_full_umbrella_values_and_validates_schema(self) -> None:
        umbrella = {
            "projectId": "prod-proj-1",
            "imageTag": "v2026.10.28",
            "ingestion_gateway_custom_image": "us-docker.pkg.dev/prod-proj-1/xmc/ig:v1",
            "secrets": {"csi": {"enabled": True, "clickhousePasswordSecretName": "csi-ch-secret"}},
            "metrics": {
                "enabled": True,
                "clickhouse": {
                    "host": "ch.xmc.svc",
                    "port": "9000",
                    "database": "mdb",
                    "user": "muser",
                },
            },
            "ingestionGatewayDeployment": {
                "enabled": True,
                "port": 8088,
                "redpandaBrokers": "[2001:db8::20]:9094,rp-1:9094",
                "topicName": "custom-metrics",
                "minPartitions": 64,
                "minReplicas": 3,
                "serviceAccountName": "ig-sa",
                "hpa": {"minReplicas": 4, "maxReplicas": 12},
                "resources": {
                    "requests": {"cpu": "250m", "memory": "512Mi"},
                    "limits": {"cpu": "1", "memory": "1Gi"},
                },
                "sasl": {"enabled": True, "mechanism": "SCRAM-SHA-512", "secretName": "rp-sasl"},
                "tls": {"enabled": True, "secretName": "rp-tls"},
            },
        }
        with tempfile.TemporaryDirectory() as tmp:
            out_path = Path(tmp) / "nested" / "profile-values.yaml"
            args = migrate.build_parser().parse_args(
                ["prepare", "-n", "xmc", "-r", "xmc", "-o", str(out_path)]
            )

            def fake_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
                if cmd[:3] == ["helm", "get", "values"]:
                    return _done(cmd, stdout=json.dumps(umbrella))
                if cmd[:2] == ["helm", "template"] and shutil.which("helm"):
                    return subprocess.run(cmd, check=True, text=True, capture_output=True)
                return _done(cmd)

            with (
                mock.patch(
                    "tools.migrations.components.migrate.verify_helm_min_version",
                    return_value="v3.15.0",
                ),
                mock.patch("tools.migrations.components.migrate.run_command", side_effect=fake_run),
            ):
                summary = migrate.run_prepare(args)
            self.assertTrue(summary["ingestion_gateway_enabled"])
            raw = out_path.read_text(encoding="utf-8")
            try:
                import yaml

                doc = yaml.safe_load(raw)
            except ImportError:
                doc = json.loads(raw)

        self.assertEqual(doc["components"]["redpanda"]["parameters"]["kafkaPort"], 9094)
        self.assertEqual(
            doc["components"]["clickhouse"]["parameters"]["secretName"], "csi-ch-secret"
        )
        ig = doc["components"]["ingestion-gateway"]
        self.assertEqual(ig["imageTag"], "v2026.10.28")
        self.assertEqual(ig["parameters"]["projectId"], "prod-proj-1")
        self.assertEqual(ig["parameters"]["hpaMinReplicas"], 4)
        self.assertEqual(ig["parameters"]["memoryLimit"], "1Gi")
        self.assertEqual(ig["parameters"]["saslSecretName"], "rp-sasl")
        self.assertEqual(ig["parameters"]["tlsSecretName"], "rp-tls")

    def test_prepare_static_zero_replicas_and_validation_cleanup(self) -> None:
        umbrella = {
            "projectId": "YOUR_GCP_PROJECT_ID",
            "metrics": "corrupted-scalar",
            "ingestionGatewayDeployment": {
                "enabled": False,
                "hpa": False,
                "replicas": 0,
                "redpandaBrokers": "rp:9092",
                "topicName": None,
                "serviceAccountName": None,
            },
        }
        with tempfile.TemporaryDirectory() as tmp:
            out_path = Path(tmp) / "profile-values.yaml"
            args = migrate.build_parser().parse_args(["prepare", "-o", str(out_path)])
            with (
                mock.patch(
                    "tools.migrations.components.migrate.verify_helm_min_version",
                    return_value="v3.14.0",
                ),
                mock.patch(
                    "tools.migrations.components.migrate.run_command",
                    side_effect=[_done(["helm"], stdout=json.dumps(umbrella)), _done(["helm"])],
                ),
            ):
                migrate.run_prepare(args)
            raw = out_path.read_text(encoding="utf-8")
            doc = (
                json.loads(raw)
                if raw.lstrip().startswith("{")
                else __import__("yaml").safe_load(raw)
            )
            ig_params = doc["components"]["ingestion-gateway"]["parameters"]
            self.assertEqual(ig_params["replicas"], 0)
            self.assertNotIn("topicName", ig_params)
            self.assertNotIn("serviceAccountName", ig_params)

            with (
                mock.patch(
                    "tools.migrations.components.migrate.verify_helm_min_version",
                    return_value="v3.14.0",
                ),
                mock.patch(
                    "tools.migrations.components.migrate.run_command",
                    side_effect=[
                        _done(["helm"], stdout="{}"),
                        subprocess.CalledProcessError(1, ["helm"], stderr="bad"),
                    ],
                ),
                self.assertRaises(HandledError),
            ):
                migrate.run_prepare(args)
            self.assertFalse(out_path.exists())

        with mock.patch.dict("sys.modules", {"yaml": None}):
            self.assertTrue(migrate._serialize_yaml({"components": {}}).startswith("{"))


class HandoffAndFinalizeTests(unittest.TestCase):
    """Tests for `handoff`, `handoff --undo`, and `finalize`."""

    def test_handoff_transfer_skip_and_guard_errors(self) -> None:
        recorded: list[list[str]] = []

        def ok_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            recorded.append(cmd)
            if cmd[:3] == ["helm", "get", "values"]:
                return _done(cmd, stdout="{}")
            if cmd[:2] == ["kubectl", "get"]:
                return _done(cmd, stdout=json.dumps(_helm_obj(cmd[-1].split("/")[1])))
            return _done(cmd)

        args = migrate.build_parser().parse_args(["handoff"])
        with mock.patch("tools.migrations.components.migrate.run_command", side_effect=ok_run):
            res = migrate.run_handoff(args)
        self.assertEqual(len(res["transferred"]), 3)
        self.assertEqual(len([c for c in recorded if c[:2] == ["kubectl", "annotate"]]), 4)

        def absent_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            return (
                _done(cmd, stdout="{}") if cmd[0] == "helm" else _done(cmd, rc=1, stderr="NotFound")
            )

        with mock.patch("tools.migrations.components.migrate.run_command", side_effect=absent_run):
            self.assertTrue(migrate.run_handoff(args)["skipped"])

        def foreign_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            return (
                _done(cmd, stdout="{}")
                if cmd[0] == "helm"
                else _done(cmd, stdout=json.dumps(_helm_obj("ig", release="other")))
            )

        with (
            mock.patch("tools.migrations.components.migrate.run_command", side_effect=foreign_run),
            self.assertRaises(HandledError),
        ):
            migrate.run_handoff(args)

    def test_handoff_undo_guards_and_restores_umbrella_owner(self) -> None:
        args = migrate.build_parser().parse_args(["handoff", "--undo"])
        with (
            mock.patch(
                "tools.migrations.components.migrate.run_command",
                return_value=_done(["kubectl"], stdout="{}"),
            ),
            self.assertRaises(HandledError),
        ):
            migrate.run_handoff(args)

        recorded: list[list[str]] = []

        def undo_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            recorded.append(cmd)
            if cmd[:2] == ["kubectl", "get"] and cmd[-1] == migrate.COMPONENT_RESOURCE:
                return _done(cmd, rc=1, stderr="doesn't have a resource type")
            if cmd[:3] == ["helm", "get", "values"]:
                return _done(cmd, stdout=json.dumps({"componentsDeployment": {"enabled": False}}))
            if cmd[:2] == ["kubectl", "get"]:
                return _done(
                    cmd, stdout=json.dumps(_helm_obj("ig", release=migrate.COMPONENT_RELEASE))
                )
            if cmd[:2] == ["helm", "status"]:
                return _done(cmd, stdout=json.dumps({"info": {"status": "deployed"}}))
            return _done(cmd)

        with mock.patch("tools.migrations.components.migrate.run_command", side_effect=undo_run):
            res = migrate.run_handoff(args)
        self.assertTrue(res["uninstalled_component_release"])
        uninstall_i = next(i for i, c in enumerate(recorded) if c[:2] == ["helm", "uninstall"])
        self.assertEqual(
            len([c for c in recorded[:uninstall_i] if c[:2] == ["kubectl", "annotate"]]), 3
        )

    def test_finalize_waits_prunes_retired_hpa_and_handles_disabled_component(self) -> None:
        recorded: list[list[str]] = []
        comp = {"spec": {"enabled": True, "parameters": {"hpaEnabled": "false"}}}

        def fin_run(cmd: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            recorded.append(cmd)
            if cmd[:2] == ["kubectl", "get"] and cmd[-1] == migrate.COMPONENT_RESOURCE:
                return _done(cmd, stdout=json.dumps(comp))
            if cmd[:2] == ["kubectl", "get"]:
                return _done(
                    cmd, stdout=json.dumps(_helm_obj("ig", release=migrate.COMPONENT_RELEASE))
                )
            if cmd[:2] == ["helm", "status"]:
                return _done(cmd, stdout=json.dumps({"info": {"status": "deployed"}}))
            return _done(cmd)

        args = migrate.build_parser().parse_args(["finalize", "--timeout-seconds", "60"])
        with mock.patch("tools.migrations.components.migrate.run_command", side_effect=fin_run):
            res = migrate.run_finalize(args)
        self.assertTrue(res["ingestion_gateway_enabled"])
        deletes = [c[-1] for c in recorded if c[:2] == ["kubectl", "delete"]]
        self.assertIn(migrate.HPA_RESOURCE, deletes)
        self.assertIn("networkpolicy/xmc-ingestion-gateway-network-policy", deletes)

        with mock.patch(
            "tools.migrations.components.migrate.run_command",
            side_effect=lambda c, **_: (
                _done(c, stdout=json.dumps({"spec": {"enabled": False}}))
                if c[:2] == ["kubectl", "get"]
                else _done(c)
            ),
        ):
            self.assertFalse(migrate.run_finalize(args)["ingestion_gateway_enabled"])


if __name__ == "__main__":
    unittest.main()
