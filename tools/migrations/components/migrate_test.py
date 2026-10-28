# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for the one-time XMC component migration CLI."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any
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


def _parse_doc(raw: str) -> dict[str, Any]:
    """Parses serialized profile-values YAML or JSON output in unit tests."""
    if raw.lstrip().startswith("{"):
        return dict(json.loads(raw))
    import yaml

    return dict(yaml.safe_load(raw))


class PrepareTests(unittest.TestCase):
    """Tests for `migrate.py prepare` value mapping, CLI bootstrap, and validation."""

    def test_direct_script_invocation_and_identifier_validation(self) -> None:
        script_path = Path(migrate.__file__).resolve()
        help_res = subprocess.run(
            [sys.executable, str(script_path), "--help"],
            check=True,
            text=True,
            capture_output=True,
        )
        self.assertIn("prepare", help_res.stdout)

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
            "secrets": {"csi": {"enabled": True, "clickhousePasswordSecretName": "gsm-ch-secret"}},
            "metrics": {
                "enabled": True,
                "clickhouse": {
                    "host": "ch.xmc.svc",
                    "port": "9000",
                    "database": "mdb",
                    "user": "muser",
                    "secretName": "k8s-ch-secret",
                },
            },
            "ingestionGatewayDeployment": {
                "enabled": True,
                "image": "   ",
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
                "sasl": {"enabled": True, "mechanism": "   ", "secretName": "rp-sasl"},
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
                    autospec=True,
                    return_value="v3.15.0",
                ),
                mock.patch("tools.migrations.components.migrate.run_command", side_effect=fake_run),
            ):
                summary = migrate.run_prepare(args)
            self.assertTrue(summary["ingestion_gateway_enabled"])
            doc = _parse_doc(out_path.read_text(encoding="utf-8"))

        self.assertEqual(doc["components"]["redpanda"]["parameters"]["kafkaPort"], 9094)
        self.assertEqual(
            doc["components"]["clickhouse"]["parameters"]["secretName"], "k8s-ch-secret"
        )
        ig = doc["components"]["ingestion-gateway"]
        self.assertEqual(ig["imageTag"], "v2026.10.28")
        self.assertEqual(ig["parameters"]["projectId"], "prod-proj-1")
        self.assertEqual(ig["parameters"]["image"], "us-docker.pkg.dev/prod-proj-1/xmc/ig:v1")
        self.assertTrue(ig["parameters"]["hpaEnabled"])
        self.assertEqual(ig["parameters"]["hpaMinReplicas"], 4)
        self.assertEqual(ig["parameters"]["memoryLimit"], "1Gi")
        self.assertEqual(ig["parameters"]["saslMechanism"], "SCRAM-SHA-256")
        self.assertEqual(ig["parameters"]["saslSecretName"], "rp-sasl")
        self.assertEqual(ig["parameters"]["tlsSecretName"], "rp-tls")

    def test_prepare_null_hpa_zero_replicas_and_validation_cleanup(self) -> None:
        umbrella = {
            "projectId": "YOUR_GCP_PROJECT_ID",
            "metrics": "corrupted-scalar",
            "ingestionGatewayDeployment": {
                "enabled": False,
                "hpa": None,
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
                    autospec=True,
                    return_value="v3.14.0",
                ),
                mock.patch(
                    "tools.migrations.components.migrate.run_command",
                    side_effect=[_done(["helm"], stdout=json.dumps(umbrella)), _done(["helm"])],
                ),
            ):
                migrate.run_prepare(args)
            ig_params = _parse_doc(out_path.read_text(encoding="utf-8"))["components"][
                "ingestion-gateway"
            ]["parameters"]
            self.assertFalse(ig_params["hpaEnabled"])
            self.assertEqual(ig_params["replicas"], 0)
            self.assertNotIn("topicName", ig_params)
            self.assertNotIn("serviceAccountName", ig_params)

            with (
                mock.patch(
                    "tools.migrations.components.migrate.verify_helm_min_version",
                    autospec=True,
                    return_value="v3.14.0",
                ),
                mock.patch(
                    "tools.migrations.components.migrate.run_command",
                    side_effect=[
                        _done(["helm"], stdout="null"),
                        subprocess.CalledProcessError(1, ["helm"], stderr="bad"),
                    ],
                ),
                self.assertRaises(HandledError),
            ):
                migrate.run_prepare(args)
            self.assertFalse(out_path.exists())

        with mock.patch.dict("sys.modules", {"yaml": None}):
            self.assertTrue(migrate._serialize_yaml({"components": {}}).startswith("{"))

        absent_hpa = migrate._build_gateway_values(
            {}, {"enabled": True, "replicas": 2}, enabled=True
        )
        self.assertFalse(absent_hpa["parameters"]["hpaEnabled"])
        self.assertEqual(absent_hpa["parameters"]["replicas"], 2)

    def test_scalar_and_json_helpers_reject_malformed_inputs(self) -> None:
        for bad_scalar in (True, False, ["rp:9092"], {"repo": "x"}):
            with self.assertRaises(HandledError):
                migrate._opt_str(bad_scalar, "field")
        for bad_int in (True, 2.5, "abc", "²", ["8080"]):
            with self.assertRaises(HandledError):
                migrate._opt_int(bad_int, "port")
        self.assertEqual(migrate._loads_obj("null", "helm get values", null_ok=True), {})
        for bad_json in ("{truncated", "null", "[]"):
            with self.assertRaises(HandledError):
                migrate._loads_obj(bad_json, "kubectl get")


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

    def test_handoff_undo_deletes_release_secrets_without_deleting_workloads(self) -> None:
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
        self.assertFalse(any(c[:2] == ["helm", "uninstall"] for c in recorded))
        self.assertTrue(
            any(
                c[:3] == ["kubectl", "delete", "secret"]
                and f"owner=helm,name={migrate.COMPONENT_RELEASE}" in c
                for c in recorded
            )
        )

    def test_finalize_waits_prunes_or_keeps_hpa_and_guards_disabled_component(self) -> None:
        args = migrate.build_parser().parse_args(["finalize", "--timeout-seconds", "60"])
        for hpa_val, expect_hpa_delete in (("false", True), ("", False)):
            recorded: list[list[str]] = []
            comp = {"spec": {"enabled": True, "parameters": {"hpaEnabled": hpa_val}}}

            def fin_run(
                cmd: list[str], _rec: list[list[str]] = recorded, _comp: object = comp, **_: object
            ) -> subprocess.CompletedProcess[str]:
                _rec.append(cmd)
                if cmd[:2] == ["kubectl", "get"] and cmd[-1] == migrate.COMPONENT_RESOURCE:
                    return _done(cmd, stdout=json.dumps(_comp))
                if cmd[:2] == ["kubectl", "get"]:
                    return _done(
                        cmd, stdout=json.dumps(_helm_obj("ig", release=migrate.COMPONENT_RELEASE))
                    )
                if cmd[:2] == ["helm", "status"]:
                    return _done(cmd, stdout=json.dumps({"info": {"status": "deployed"}}))
                return _done(cmd)

            with mock.patch("tools.migrations.components.migrate.run_command", side_effect=fin_run):
                res = migrate.run_finalize(args)
            self.assertTrue(res["ingestion_gateway_enabled"])
            deletes = [c[-1] for c in recorded if c[:2] == ["kubectl", "delete"]]
            self.assertEqual(migrate.HPA_RESOURCE in deletes, expect_hpa_delete)
            self.assertIn("networkpolicy/xmc-ingestion-gateway-network-policy", deletes)

        def disabled_with_live_deploy(
            cmd: list[str], **_: object
        ) -> subprocess.CompletedProcess[str]:
            if cmd[-1] == migrate.COMPONENT_RESOURCE:
                return _done(cmd, stdout=json.dumps({"spec": {"enabled": False}}))
            return _done(cmd, stdout=json.dumps(_helm_obj("ig")))

        with (
            mock.patch(
                "tools.migrations.components.migrate.run_command",
                side_effect=disabled_with_live_deploy,
            ),
            self.assertRaises(HandledError),
        ):
            migrate.run_finalize(args)

        def disabled_without_deploy(
            cmd: list[str], **_: object
        ) -> subprocess.CompletedProcess[str]:
            if cmd[-1] == migrate.COMPONENT_RESOURCE:
                return _done(cmd, stdout=json.dumps({"spec": {"enabled": False}}))
            if cmd[:2] == ["kubectl", "get"]:
                return _done(cmd, rc=1, stderr="NotFound")
            return _done(cmd)

        with mock.patch(
            "tools.migrations.components.migrate.run_command", side_effect=disabled_without_deploy
        ):
            self.assertFalse(migrate.run_finalize(args)["ingestion_gateway_enabled"])


if __name__ == "__main__":
    unittest.main()
