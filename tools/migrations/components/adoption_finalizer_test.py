# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for adoption_finalizer."""

import json
import subprocess
import unittest
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.migrations.components import adoption_finalizer


def _owned_meta(name: str, ns: str = "xmc") -> dict[str, Any]:
    return {
        "name": name,
        "generation": 2,
        "labels": {"app.kubernetes.io/managed-by": "Helm"},
        "annotations": {
            "meta.helm.sh/release-name": "xmc-cmp-ingestion-gateway",
            "meta.helm.sh/release-namespace": ns,
        },
    }


def _rendered_objects() -> dict[tuple[str, str], dict[str, Any]]:
    dep = {
        "kind": "Deployment",
        "metadata": _owned_meta("ingestion-gateway-deployment"),
        "spec": {
            "replicas": 2,
            "template": {
                "spec": {
                    "containers": [
                        {
                            "name": "ingestion-gateway",
                            "image": "gcr.io/p/ig:1.0",
                            "args": ["-port=8080", "-enable_components=true"],
                        }
                    ],
                    "volumes": [{"name": "kafka-tls-credentials"}],
                }
            },
        },
        "status": {"observedGeneration": 2},
    }
    svc = {
        "kind": "Service",
        "metadata": _owned_meta("ingestion-gateway-service"),
        "spec": {"ports": [{"name": "http", "port": 8080, "targetPort": 8080}]},
    }
    hpa = {
        "kind": "HorizontalPodAutoscaler",
        "metadata": _owned_meta("ingestion-gateway-hpa"),
        "spec": {"minReplicas": 3, "maxReplicas": 10},
    }
    return {
        ("deployment", "ingestion-gateway-deployment"): dep,
        ("service", "ingestion-gateway-service"): svc,
        ("horizontalpodautoscaler", "ingestion-gateway-hpa"): hpa,
    }


class TestAdoptionFinalizer(unittest.TestCase):
    """Tests for Component readiness polling, drift checks, keep stripping, and HTTP probing."""

    @patch("tools.migrations.components.adoption_finalizer.time.sleep")
    @patch("tools.migrations.components.adoption_finalizer.fetch_live_resource")
    def test_wait_for_component_ready_checks_observed_generation(
        self, mock_live: MagicMock, _mock_sleep: MagicMock
    ) -> None:
        stale_cr = {
            "metadata": {"generation": 2},
            "status": {
                "conditions": [
                    {"type": "Ready", "status": "True", "observedGeneration": 1},
                    {"type": "Synced", "status": "True", "observedGeneration": 2},
                ]
            },
        }
        fresh_cr = {
            "metadata": {"generation": 2},
            "status": {
                "conditions": [
                    {"type": "Ready", "status": "True", "observedGeneration": 2},
                    {"type": "Synced", "status": "True", "observedGeneration": 2},
                ]
            },
        }
        mock_live.side_effect = [stale_cr, fresh_cr]
        adoption_finalizer._wait_for_component_ready("xmc", timeout_sec=5, poll_interval_sec=0.01)
        self.assertEqual(mock_live.call_count, 2)

        mock_live.side_effect = None
        mock_live.return_value = stale_cr
        with self.assertRaises(HandledError):
            adoption_finalizer._wait_for_component_ready("xmc", timeout_sec=0, poll_interval_sec=0)

    @patch("tools.migrations.components.adoption_finalizer.urllib.request.urlopen")
    @patch("tools.migrations.components.adoption_finalizer.annotate_resource")
    @patch("tools.migrations.components.adoption_finalizer.run_command")
    @patch("tools.migrations.components.adoption_finalizer.fetch_live_resource")
    @patch("tools.migrations.components.adoption_finalizer.fetch_rendered_release_objects")
    @patch(
        "tools.migrations.components.adoption_finalizer.fetch_helm_release_status",
        return_value="deployed",
    )
    def test_finalize_adoption_happy_path(
        self,
        _mock_status: MagicMock,
        mock_rendered: MagicMock,
        mock_live: MagicMock,
        mock_run: MagicMock,
        mock_annotate: MagicMock,
        mock_urlopen: MagicMock,
    ) -> None:
        objs = _rendered_objects()
        mock_rendered.return_value = objs
        cr_ready = {
            "metadata": {"generation": 2},
            "status": {
                "conditions": [
                    {"type": "Ready", "status": "True", "observedGeneration": 2},
                    {"type": "Synced", "status": "True", "observedGeneration": 2},
                ]
            },
        }
        mock_live.side_effect = [
            cr_ready,
            objs[("deployment", "ingestion-gateway-deployment")],
            objs[("service", "ingestion-gateway-service")],
            objs[("horizontalpodautoscaler", "ingestion-gateway-hpa")],
            {"kind": "NetworkPolicy"},
        ]
        pods_json = json.dumps(
            {
                "items": [
                    {"status": {"phase": "Succeeded"}},
                    {
                        "metadata": {"name": "ig-pod-1"},
                        "status": {
                            "phase": "Running",
                            "containerStatuses": [{"ready": True, "state": {"running": {}}}],
                        },
                    },
                ]
            }
        )
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout="rolled out"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout=pods_json),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="deleted"),
        ]
        resp = MagicMock()
        resp.status = 202
        resp.__enter__.return_value = resp
        mock_urlopen.return_value = resp

        got = adoption_finalizer.finalize_adoption(
            "xmc", "xmc", metrics_url="http://127.0.0.1:8080/api/v1/metrics", test_xid=42
        )
        self.assertEqual(got["active_pods"], 1)
        self.assertTrue(got["legacy_netpol_deleted"])
        self.assertEqual(got["probe_http_status"], 202)
        self.assertEqual(mock_annotate.call_count, 3)

    @patch("tools.migrations.components.adoption_finalizer.run_command")
    def test_verify_rollout_and_probe_validation_errors(self, mock_run: MagicMock) -> None:
        with self.assertRaises(HandledError):
            adoption_finalizer.finalize_adoption("xmc", "xmc", metrics_url="", skip_probe=False)
        with self.assertRaises(HandledError):
            adoption_finalizer._probe_metrics_endpoint("file:///etc/passwd", 0)

        # Empty active pods when expected_replicas > 0 fails closed.
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0),
            subprocess.CompletedProcess(args=[], returncode=0, stdout='{"items": []}'),
        ]
        with self.assertRaises(HandledError):
            adoption_finalizer._verify_rollout_and_pods("xmc", 10, expected_replicas=2)

        # Pending pod with empty containerStatuses fails closed.
        pending = json.dumps(
            {"items": [{"metadata": {"name": "p1"}, "status": {"phase": "Pending"}}]}
        )
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0),
            subprocess.CompletedProcess(args=[], returncode=0, stdout=pending),
        ]
        with self.assertRaises(HandledError):
            adoption_finalizer._verify_rollout_and_pods("xmc", 10, expected_replicas=1)

    @patch("tools.migrations.components.adoption_finalizer.fetch_live_resource")
    @patch("tools.migrations.components.adoption_finalizer.fetch_rendered_release_objects")
    @patch(
        "tools.migrations.components.adoption_finalizer.fetch_helm_release_status",
        return_value="deployed",
    )
    def test_drift_detection_catches_missing_enable_components_flag(
        self, _mock_status: MagicMock, mock_rendered: MagicMock, mock_live: MagicMock
    ) -> None:
        objs = _rendered_objects()
        mock_rendered.return_value = objs
        drifted_dep = json.loads(json.dumps(objs[("deployment", "ingestion-gateway-deployment")]))
        drifted_dep["spec"]["template"]["spec"]["containers"][0]["args"] = ["-port=8080"]
        mock_live.side_effect = [drifted_dep, objs[("service", "ingestion-gateway-service")]]
        with self.assertRaises(HandledError):
            adoption_finalizer._verify_adopted_objects_and_drift("xmc")


if __name__ == "__main__":
    unittest.main()
