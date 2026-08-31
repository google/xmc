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

"""Helm chart template sanitization and AST transformation for local KinD development."""

from __future__ import annotations

import logging
import os
import re
import shutil
from pathlib import Path

_LOGGER = logging.getLogger("xmc.dev_env.chart_sanitizer")

UNSUPPORTED_LOCAL_API_GROUPS: tuple[str, ...] = (
    "cloud.google.com",
    "networking.gke.io",
    "secrets-store.csi.x-k8s.io",
    "cert-manager.io",
    "acme.cert-manager.io",
    "gateway.networking.k8s.io",
    "autoscaling.k8s.io",
    "kueue.x-k8s.io",
)

UNSUPPORTED_LOCAL_KINDS: tuple[str, ...] = (
    "BackendConfig",
    "ManagedCertificate",
    "HealthCheckPolicy",
    "SecretProviderClass",
    "VerticalPodAutoscaler",
    "HorizontalPodAutoscaler",
    "Gateway",
    "HTTPRoute",
    "Issuer",
    "ClusterIssuer",
    "Certificate",
    "LocalQueue",
    "ClusterQueue",
    "ResourceFlavor",
    "WorkloadPriorityClass",
)


def is_unsupported_kubernetes_doc(doc: str) -> bool:
    """Checks if a YAML document declares an unsupported cloud-only API group or Kind."""
    api_match = re.search(r'^\s*apiVersion:\s*["\']?([^"\'\s]+)', doc, re.MULTILINE)
    kind_match = re.search(r'^\s*kind:\s*["\']?([^"\'\s]+)', doc, re.MULTILINE)

    if api_match:
        api_val = api_match.group(1)
        if any(group in api_val for group in UNSUPPORTED_LOCAL_API_GROUPS):
            return True

    if kind_match:
        kind_val = kind_match.group(1)
        if kind_val in UNSUPPORTED_LOCAL_KINDS:
            return True

    return False


def make_tree_writable(dir_path: Path) -> None:
    """Recursively adds write permissions to all directories and files in tree."""
    if not dir_path.exists():
        return
    try:
        os.chmod(dir_path, 0o755)
    except OSError:
        pass
    for root, dirs, files in os.walk(dir_path):
        for d in dirs:
            try:
                os.chmod(os.path.join(root, d), 0o755)
            except OSError:
                pass
        for f in files:
            try:
                os.chmod(os.path.join(root, f), 0o644)
            except OSError:
                pass


class ChartSanitizer:
    """Copies and sanitizes production Helm chart templates for local KinD execution."""

    def __init__(self, source_chart: Path, target_dir: Path) -> None:
        """Initializes ChartSanitizer with source and target chart paths."""
        self.source_chart = source_chart
        self.target_dir = target_dir

    def sanitize_templates(self, templates_dir: Path) -> None:
        """Dynamically filters out cloud-only manifests based on apiVersion and kind."""
        if not templates_dir.is_dir():
            return

        for path in list(templates_dir.rglob("*.yaml")) + list(templates_dir.rglob("*.yml")):
            try:
                content = path.read_text(encoding="utf-8")
                docs = re.split(r"(?m)^---\s*$", content)
                kept_docs = [d for d in docs if d.strip() and not is_unsupported_kubernetes_doc(d)]
                path.chmod(path.stat().st_mode | 0o200)
                if not kept_docs:
                    _LOGGER.debug("Pruning cloud-only template file: %s", path.name)
                    path.unlink()
                else:
                    path.write_text("\n---\n".join(kept_docs) + "\n", encoding="utf-8")
            except OSError as e:
                _LOGGER.warning("Failed to sanitize template file %s: %s", path, e)

        # Remove empty subdirectories recursively
        for directory in sorted(templates_dir.rglob("*"), key=lambda x: len(str(x)), reverse=True):
            if directory.is_dir() and not any(directory.iterdir()):
                try:
                    directory.rmdir()
                except OSError:
                    pass

    def patch_image_pull_policy(self, chart_dir: Path) -> int:
        """Replaces imagePullPolicy Always with IfNotPresent in local templates."""
        total_patched = 0
        for path in chart_dir.rglob("*.yaml"):
            try:
                text = path.read_text(encoding="utf-8")
                patched, count = re.subn(
                    r'\bimagePullPolicy:\s*["\']?Always\b["\']?',
                    "imagePullPolicy: IfNotPresent",
                    text,
                )
                if count > 0:
                    total_patched += count
                    path.chmod(path.stat().st_mode | 0o200)
                    path.write_text(patched, encoding="utf-8")
            except OSError as e:
                _LOGGER.warning("Failed to process or patch file %s: %s", path, e)
        _LOGGER.debug("Patched imagePullPolicy: IfNotPresent across %d locations", total_patched)
        return total_patched

    def prepare_sanitized_chart(self) -> Path:
        """Copies Helm chart to target directory, prunes cloud resources, and patches images."""
        _LOGGER.debug("Preparing local Helm chart copy at %s", self.target_dir)
        if self.target_dir.exists():
            make_tree_writable(self.target_dir)
            shutil.rmtree(self.target_dir)
        if not self.source_chart.is_dir():
            raise FileNotFoundError(
                f"Source Helm chart directory not found at '{self.source_chart}'"
            )
        shutil.copytree(self.source_chart, self.target_dir)
        make_tree_writable(self.target_dir)

        templates_dir = self.target_dir / "templates"
        self.sanitize_templates(templates_dir)
        self.patch_image_pull_policy(self.target_dir)
        return self.target_dir
