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

"""Installation of the pinned upstream JobSet and Kueue controllers into the KinD cluster."""

from __future__ import annotations

import dataclasses
import logging
import os

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.polling import poll_until

logger = logging.getLogger("xmc_e2e_runner")

_WAIT_TIMEOUT = "--timeout=60s"
_ROLLOUT_TIMEOUT = "--timeout=180s"
_CA_BUNDLE_TIMEOUT_SECONDS = 60.0
_CA_BUNDLE_POLL_INTERVAL_SECONDS = 2.0
# JSONPath of the CA bundle that a controller injects into its webhook configurations.
_WEBHOOK_CA = "{.webhooks[0].clientConfig.caBundle}"
# JSONPath of the CA bundle that a controller injects into the conversion webhook of a CRD.
_CRD_CONVERSION_CA = "{.spec.conversion.webhook.clientConfig.caBundle}"


@dataclasses.dataclass(frozen=True)
class ControllerRelease:
    """A pinned upstream controller release and the signals that it serves its APIs.

    Attributes:
        name: Controller name used in logs.
        manifest_target: Target in //test/e2e/manifests holding the release manifest.
        namespace: Namespace of the controller manager deployment.
        deployment: Name of the controller manager deployment.
        crds: CustomResourceDefinitions that must be established.
        api_services: Aggregated APIServices that must be Available.
        ca_bundles: (resource, JSONPath) pairs where the controller injects its webhook CA.
    """

    name: str
    manifest_target: str
    namespace: str
    deployment: str
    crds: tuple[str, ...]
    api_services: tuple[str, ...] = ()
    ca_bundles: tuple[tuple[str, str], ...] = ()


JOBSET_RELEASE = ControllerRelease(
    name="JobSet",
    manifest_target="jobset_manifests",
    namespace="jobset-system",
    deployment="jobset-controller-manager",
    crds=("jobsets.jobset.x-k8s.io",),
    ca_bundles=(
        ("mutatingwebhookconfiguration/jobset-mutating-webhook-configuration", _WEBHOOK_CA),
        ("validatingwebhookconfiguration/jobset-validating-webhook-configuration", _WEBHOOK_CA),
    ),
)

KUEUE_RELEASE = ControllerRelease(
    name="Kueue",
    manifest_target="kueue_manifests",
    namespace="kueue-system",
    deployment="kueue-controller-manager",
    crds=tuple(
        f"{plural}.kueue.x-k8s.io"
        for plural in (
            "clusterqueues",
            "localqueues",
            "resourceflavors",
            "workloadpriorityclasses",
            "workloads",
        )
    ),
    api_services=("v1beta1.visibility.kueue.x-k8s.io", "v1beta2.visibility.kueue.x-k8s.io"),
    ca_bundles=(
        ("mutatingwebhookconfiguration/kueue-mutating-webhook-configuration", _WEBHOOK_CA),
        ("validatingwebhookconfiguration/kueue-validating-webhook-configuration", _WEBHOOK_CA),
        # XMC reads these resources as v1beta1, which the API server converts through Kueue's
        # conversion webhook.
        ("crd/clusterqueues.kueue.x-k8s.io", _CRD_CONVERSION_CA),
        ("crd/localqueues.kueue.x-k8s.io", _CRD_CONVERSION_CA),
        ("crd/workloads.kueue.x-k8s.io", _CRD_CONVERSION_CA),
    ),
)

# Kueue v0.18.2 defers setting up an integration whose API is missing when it starts and
# retries with a backoff of up to two minutes (jobframework/setup.go), so the JobSet CRD is
# established before Kueue is applied.
CONTROLLER_RELEASES = (JOBSET_RELEASE, KUEUE_RELEASE)


class ControllerInstaller:
    """Installs the controller releases that XMC work units depend on."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.context = f"kind-{config.cluster_name}"

    def install_all(self) -> None:
        """Applies every release in order, then waits until all of them serve.

        The controllers start concurrently; each release is applied only after the CRDs of
        the previous one are established.

        Raises:
            subprocess.CalledProcessError: If kubectl fails or a readiness wait times out.
            PollTimeoutError: If a controller does not inject its webhook CA bundle in time.
        """
        for release in CONTROLLER_RELEASES:
            self._apply(release)
        for release in CONTROLLER_RELEASES:
            self._wait_until_serving(release)

    def _apply(self, release: ControllerRelease) -> None:
        """Applies a release manifest server-side and waits until its CRDs are established."""
        manifest = self._resolve_manifest(release.manifest_target)
        logger.info("Installing the %s controller from %s...", release.name, manifest)
        self._kubectl("apply", "--server-side", "--force-conflicts", "-f", manifest)
        crds = [f"crd/{crd}" for crd in release.crds]
        self._kubectl("wait", "--for=condition=established", _WAIT_TIMEOUT, *crds)

    def _wait_until_serving(self, release: ControllerRelease) -> None:
        """Waits until the controller is rolled out and its APIs and webhooks can serve."""
        deployment = f"deployment/{release.deployment}"
        self._kubectl("rollout", "status", deployment, "-n", release.namespace, _ROLLOUT_TIMEOUT)
        if release.api_services:
            services = [f"apiservice/{service}" for service in release.api_services]
            self._kubectl("wait", "--for=condition=Available", _WAIT_TIMEOUT, *services)
        for resource, jsonpath in release.ca_bundles:
            self._wait_for_ca_bundle(resource, jsonpath)
        logger.info("✓ The %s controller is serving.", release.name)

    def _wait_for_ca_bundle(self, resource: str, jsonpath: str) -> None:
        """Polls until the controller has injected its CA bundle at jsonpath of resource.

        A failing kubectl raises CalledProcessError, which poll_until logs and retries.
        """
        output = f"jsonpath={jsonpath}"

        def probe() -> str | None:
            result = run_cmd(
                ["kubectl", "--context", self.context, "get", resource, "-o", output],
                capture_output=True,
            )
            return result.stdout.strip() or None

        poll_until(
            probe,
            f"CA bundle injection into {resource}",
            _CA_BUNDLE_TIMEOUT_SECONDS,
            _CA_BUNDLE_POLL_INTERVAL_SECONDS,
        )

    def _kubectl(self, *args: str) -> None:
        """Runs a kubectl command against the KinD cluster, raising on failure."""
        run_cmd(["kubectl", "--context", self.context, *args])

    def _resolve_manifest(self, target: str) -> str:
        """Returns the path of a //test/e2e/manifests output, building the target if absent."""
        manifest_rel = os.path.join("test", "e2e", "manifests", f"{target}.yaml")
        default_path = os.path.join(self.cfg.workspace_root, "bazel-bin", manifest_rel)
        if _is_non_empty_file(default_path):
            return default_path

        logger.info("Building //test/e2e/manifests:%s with Bazel...", target)
        run_cmd(["bazel", "build", f"//test/e2e/manifests:{target}"], cwd=self.cfg.workspace_root)
        if _is_non_empty_file(default_path):
            return default_path
        return os.path.join(self._bazel_bin(), manifest_rel)

    def _bazel_bin(self) -> str:
        """Returns the bazel-bin directory reported by 'bazel info bazel-bin'."""
        result = run_cmd(
            ["bazel", "info", "bazel-bin"],
            cwd=self.cfg.workspace_root,
            capture_output=True,
        )
        lines = result.stdout.strip().splitlines()
        bazel_bin = lines[-1] if lines else ""
        if not bazel_bin:
            raise RuntimeError("Failed to resolve bazel-bin directory via 'bazel info bazel-bin'")
        return bazel_bin


def _is_non_empty_file(path: str) -> bool:
    """Returns whether path is a regular file with content."""
    return os.path.isfile(path) and os.path.getsize(path) > 0
