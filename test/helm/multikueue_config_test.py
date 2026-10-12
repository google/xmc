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

"""Verifies the Helm chart renders MultiKueue CRs and in-cluster k3s worker templates correctly."""

import unittest

from chart_test_lib import render_chart, render_template, require_helm

MULTIKUEUE_QUEUE_TEMPLATE = "templates/kueue-dev/multikueue-cpu-queue.yaml"
MULTIKUEUE_WORKER_TEMPLATE = "templates/kueue-dev/multikueue-worker.yaml"
ACTUATOR_CLUSTERROLE_TEMPLATE = "templates/actuator/clusterrole.yaml"


class MultiKueueConfigTest(unittest.TestCase):
    """Asserts MultiKueue queue CRs, worker deployment, and Actuator RBAC render properly."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def _assert_no_root_flow_mapping(self, manifest: str) -> None:
        for doc in manifest.split("---"):
            lines = [
                ln.strip()
                for ln in doc.splitlines()
                if ln.strip() and not ln.strip().startswith("#")
            ]
            if lines:
                self.assertFalse(lines[0].startswith("{"), f"Root flow mapping found: {lines[0]}")

    def test_multikueue_disabled_by_default(self) -> None:
        self.assertNotIn("kind:", render_template(MULTIKUEUE_QUEUE_TEMPLATE, {}))
        self.assertNotIn("kind:", render_template(MULTIKUEUE_WORKER_TEMPLATE, {}))

    def test_multikueue_queue_renders_when_enabled(self) -> None:
        manifest = render_template(
            MULTIKUEUE_QUEUE_TEMPLATE,
            {
                "projectId": "test-proj",
                "clusterLocation": "us-central1",
                "kueueDev.multikueue.enabled": "true",
                "kueueDev.multikueue.inClusterWorker.enabled": "true",
            },
        )
        self.assertIn("kind: LocalQueue", manifest)
        self.assertIn('"multikueue-cpu-queue"', manifest)
        self.assertIn('"multikueue-cpu-cluster-queue"', manifest)
        self.assertIn("kind: ClusterQueue", manifest)
        self.assertIn('name: "cpu-flavor"', manifest)
        self.assertNotIn("default-flavor", manifest)
        self.assertIn("admissionChecksStrategy:", manifest)
        self.assertIn('"multikueue-admission-check"', manifest)
        self.assertIn("kind: AdmissionCheck", manifest)
        self.assertIn("controllerName: kueue.x-k8s.io/multikueue", manifest)
        self.assertIn("kind: MultiKueueConfig", manifest)
        self.assertIn("kind: MultiKueueCluster", manifest)
        self.assertIn("labels:", manifest)
        self.assertNotIn("annotations:", manifest)
        self.assertIn('xmc.google.com/project: "test-proj"', manifest)
        self.assertIn('xmc.google.com/location: "us-central1"', manifest)
        self.assertIn('xmc.google.com/cluster-name: "xmc-nightly-worker-1"', manifest)
        self.assertIn("clusterSource:", manifest)
        self.assertIn('"multikueue-worker-kubeconfig"', manifest)
        self._assert_no_root_flow_mapping(manifest)

        # Without inClusterWorker.enabled or explicit kubeconfigSecretName, queue does not render a dangling MultiKueueCluster
        no_worker_manifest = render_template(
            MULTIKUEUE_QUEUE_TEMPLATE,
            {"kueueDev.multikueue.enabled": "true"},
        )
        self.assertNotIn("kind:", no_worker_manifest)

        # With explicit external kubeconfigSecretName, queue renders without inClusterWorker
        ext_manifest = render_template(
            MULTIKUEUE_QUEUE_TEMPLATE,
            {
                "kueueDev.multikueue.enabled": "true",
                "kueueDev.multikueue.kubeconfigSecretName": "external-worker-kubeconfig",
            },
        )
        self.assertIn('"external-worker-kubeconfig"', ext_manifest)

    def test_multikueue_worker_renders_when_enabled_and_skips_in_dev_env(self) -> None:
        manifest = render_template(
            MULTIKUEUE_WORKER_TEMPLATE,
            {
                "kueueDev.multikueue.enabled": "true",
                "kueueDev.multikueue.inClusterWorker.enabled": "true",
            },
        )
        self.assertIn("kind: Deployment", manifest)
        self.assertIn("name: multikueue-worker", manifest)
        self.assertIn("rancher/k3s:v1.32.3-k3s1", manifest)
        self.assertNotIn("k3s kubectl", manifest)
        self.assertIn("name: k3s-server", manifest)
        self.assertIn("name: bootstrap-sync", manifest)
        self.assertIn("jobset/releases/download/v0.12.0/manifests.yaml", manifest)
        self.assertIn("kueue/releases/download/v0.18.2/manifests.yaml", manifest)
        self.assertIn("kind: WorkloadPriorityClass", manifest)
        self.assertIn("kind: ResourceFlavor", manifest)
        self.assertIn("kind: ClusterQueue", manifest)
        self.assertIn("kind: LocalQueue", manifest)
        self.assertIn("--write-kubeconfig=/k3s-config/k3s.yaml", manifest)
        self.assertIn("create secret generic multikueue-worker-kubeconfig", manifest)
        self.assertIn("exec sleep infinity", manifest)
        self.assertNotIn('"configmaps"', manifest)
        self.assertNotIn("kueue-manager-config", manifest)
        self._assert_no_root_flow_mapping(manifest)

        dev_manifest = render_chart(
            {
                "isDevEnvironment": "true",
                "kueueDev.multikueue.enabled": "true",
                "kueueDev.multikueue.inClusterWorker.enabled": "true",
            }
        )
        self.assertNotIn("name: multikueue-worker-bootstrap", dev_manifest)
        self.assertNotIn('"multikueue-cpu-queue"', dev_manifest)

    def test_actuator_clusterrole_grants_kueue_queue_and_admissioncheck_read(self) -> None:
        manifest = render_template(ACTUATOR_CLUSTERROLE_TEMPLATE, {})
        self.assertIn('"localqueues"', manifest)
        self.assertIn('"admissionchecks"', manifest)


if __name__ == "__main__":
    unittest.main()
