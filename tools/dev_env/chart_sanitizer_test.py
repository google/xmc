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

"""Unit tests for ChartSanitizer."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from tools.dev_env.chart_sanitizer import (
    ChartSanitizer,
    is_unsupported_kubernetes_doc,
    make_tree_writable,
)


class TestChartSanitizer(unittest.TestCase):
    """Test suite for ChartSanitizer and AST transformation utilities."""

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.tmp_dir.name)
        self.source_chart = self.temp_path / "deploy" / "helm"
        self.target_dir = self.temp_path / "sanitized_helm"
        self.sanitizer = ChartSanitizer(
            source_chart=self.source_chart,
            target_dir=self.target_dir,
        )

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    def test_is_unsupported_kubernetes_doc(self) -> None:
        # Standard Kubernetes resources (should NOT be unsupported)
        self.assertFalse(is_unsupported_kubernetes_doc("apiVersion: v1\nkind: Service"))
        self.assertFalse(is_unsupported_kubernetes_doc("apiVersion: apps/v1\nkind: Deployment"))
        self.assertFalse(is_unsupported_kubernetes_doc("apiVersion: v1\nkind: ConfigMap"))
        self.assertFalse(
            is_unsupported_kubernetes_doc("apiVersion: rbac.authorization.k8s.io/v1\nkind: Role")
        )

        # Cloud-specific API groups
        self.assertTrue(
            is_unsupported_kubernetes_doc("apiVersion: cloud.google.com/v1\nkind: BackendConfig")
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: networking.gke.io/v1\nkind: ManagedCertificate"
            )
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: secrets-store.csi.x-k8s.io/v1\nkind: SecretProviderClass"
            )
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc("apiVersion: cert-manager.io/v1\nkind: Issuer")
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute"
            )
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc("apiVersion: kueue.x-k8s.io/v1beta1\nkind: LocalQueue")
        )

        # Cloud-specific Kinds
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler"
            )
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: autoscaling.k8s.io/v1\nkind: VerticalPodAutoscaler"
            )
        )
        self.assertTrue(
            is_unsupported_kubernetes_doc(
                "apiVersion: kueue.x-k8s.io/v1beta1\nkind: WorkloadPriorityClass"
            )
        )

    def test_make_tree_writable(self) -> None:
        test_dir = self.temp_path / "readonly_test"
        test_dir.mkdir(parents=True, exist_ok=True)
        sub_file = test_dir / "file.txt"
        sub_file.write_text("hello", encoding="utf-8")
        sub_dir = test_dir / "subdir"
        sub_dir.mkdir(parents=True, exist_ok=True)

        make_tree_writable(test_dir)
        self.assertTrue(test_dir.exists())
        self.assertTrue(sub_file.exists())

    def test_prepare_sanitized_chart_pruning_and_patching(self) -> None:
        templates = self.source_chart / "templates"
        (templates / "ess").mkdir(parents=True, exist_ok=True)
        (templates / "certs").mkdir(parents=True, exist_ok=True)
        (templates / "custom_dir").mkdir(parents=True, exist_ok=True)

        # Create single-doc cloud prune targets
        (templates / "certs" / "issuer.yaml").write_text(
            "apiVersion: cert-manager.io/v1\nkind: Issuer\nmetadata:\n  name: issuer"
        )
        (templates / "ess" / "gateway.yaml").write_text(
            "apiVersion: gateway.networking.k8s.io/v1\nkind: Gateway\nmetadata:\n  name: gw"
        )
        (templates / "custom_dir" / "new_backend_config.yaml").write_text(
            "apiVersion: cloud.google.com/v1\nkind: BackendConfig\nmetadata:\n  name: bc"
        )

        # Create multi-doc template with mixed resources
        (templates / "ess" / "mixed.yaml").write_text(
            "apiVersion: v1\nkind: Service\nmetadata:\n  name: svc\n---\n"
            "apiVersion: cloud.google.com/v1\nkind: BackendConfig\nmetadata:\n  name: bc"
        )

        # Surviving deployment with imagePullPolicy Always
        (templates / "ess" / "deployment.yaml").write_text(
            "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: deploy\n"
            "spec:\n  template:\n    spec:\n      containers:\n      - imagePullPolicy: Always"
        )

        prepared = self.sanitizer.prepare_sanitized_chart()
        self.assertTrue(prepared.is_dir())
        out_templates = prepared / "templates"

        # Cloud prune targets and newly empty directories should be removed
        self.assertFalse((out_templates / "certs").exists())
        self.assertFalse((out_templates / "custom_dir").exists())
        self.assertFalse((out_templates / "ess" / "gateway.yaml").exists())

        # Multi-doc template should retain only the Service
        mixed_file = out_templates / "ess" / "mixed.yaml"
        self.assertTrue(mixed_file.exists())
        mixed_content = mixed_file.read_text(encoding="utf-8")
        self.assertIn("kind: Service", mixed_content)
        self.assertNotIn("BackendConfig", mixed_content)

        # Surviving template should have imagePullPolicy patched
        surviving = out_templates / "ess" / "deployment.yaml"
        self.assertTrue(surviving.exists())
        content = surviving.read_text(encoding="utf-8")
        self.assertIn("imagePullPolicy: IfNotPresent", content)
        self.assertNotIn("imagePullPolicy: Always", content)

    def test_prepare_sanitized_chart_missing_source_raises(self) -> None:
        sanitizer = ChartSanitizer(
            source_chart=self.temp_path / "nonexistent",
            target_dir=self.target_dir,
        )
        with self.assertRaises(FileNotFoundError):
            sanitizer.prepare_sanitized_chart()

    def test_is_unsupported_kubernetes_doc_nested_keys_ignored(self) -> None:
        doc = (
            "apiVersion: v1\n"
            "kind: ConfigMap\n"
            "metadata:\n"
            "  name: my-config\n"
            "data:\n"
            "  nested.yaml: |\n"
            "    apiVersion: cert-manager.io/v1\n"
            "    kind: Issuer\n"
        )
        self.assertFalse(is_unsupported_kubernetes_doc(doc))

    def test_sanitize_templates_catches_decode_error(self) -> None:
        templates = self.source_chart / "templates"
        templates.mkdir(parents=True, exist_ok=True)
        corrupt_file = templates / "corrupt.yaml"
        corrupt_file.write_bytes(b"\x80\xff\xfe\xfd")

        # Should not raise UnicodeDecodeError / ValueError
        self.sanitizer.sanitize_templates(templates)

    def test_patch_image_pull_policy_handles_yml_and_decode_error(self) -> None:
        chart_dir = self.source_chart
        (chart_dir / "templates").mkdir(parents=True, exist_ok=True)
        yml_file = chart_dir / "templates" / "service.yml"
        yml_file.write_text("imagePullPolicy: Always\n", encoding="utf-8")
        corrupt_file = chart_dir / "templates" / "corrupt.yml"
        corrupt_file.write_bytes(b"\x80\xff\xfe\xfd")

        patched = self.sanitizer.patch_image_pull_policy(chart_dir)
        self.assertEqual(patched, 1)
        self.assertIn("imagePullPolicy: IfNotPresent", yml_file.read_text(encoding="utf-8"))
