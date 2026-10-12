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

"""Verifies the MultiKueue CI addon manifests and default Helm chart isolation."""

import os
import unittest

from chart_test_lib import find_repo_file, render_chart, require_helm

WORKER_MANIFEST_PATH = os.path.join(
    "deploy", "addons", "multikueue", "manifests", "multikueue-worker.yaml"
)
WORKER_QUEUES_MANIFEST_PATH = os.path.join(
    "deploy", "addons", "multikueue", "manifests", "worker-queues.yaml"
)
MANAGEMENT_MULTIKUEUE_MANIFEST_PATH = os.path.join(
    "deploy", "addons", "multikueue", "manifests", "management-multikueue.yaml"
)
SETUP_SCRIPT_PATH = os.path.join("deploy", "addons", "multikueue", "setup_multikueue.sh")


class MultiKueueConfigTest(unittest.TestCase):
    """Asserts MultiKueue addon manifests, customer chart isolation, and Actuator RBAC."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def _read_repo_file(self, relative_path: str) -> str:
        with open(find_repo_file(relative_path), encoding="utf-8") as handle:
            return handle.read()

    def _assert_no_root_flow_mapping(self, manifest: str) -> None:
        for doc in manifest.split("---"):
            lines = [
                ln.strip()
                for ln in doc.splitlines()
                if ln.strip() and not ln.strip().startswith("#")
            ]
            if lines:
                self.assertFalse(
                    lines[0].startswith("{"),
                    f"Root flow mapping found: {lines[0]}",
                )

    def test_customer_helm_chart_excludes_multikueue_test_resources(self) -> None:
        manifest = render_chart({})
        self.assertNotIn("multikueue-worker", manifest)
        self.assertNotIn("multikueue-cpu-queue", manifest)
        self.assertNotIn("xmc-nightly-worker-1", manifest)

    def test_management_multikueue_addon_manifest_is_valid(self) -> None:
        manifest = self._read_repo_file(MANAGEMENT_MULTIKUEUE_MANIFEST_PATH)
        self.assertIn("kind: Namespace", manifest)
        self.assertIn("kind: LimitRange", manifest)
        self.assertIn("kind: LocalQueue", manifest)
        self.assertIn("name: multikueue-cpu-queue", manifest)
        self.assertIn("clusterQueue: multikueue-cpu-cluster-queue", manifest)
        self.assertIn("kind: ClusterQueue", manifest)
        self.assertIn("name: cpu-flavor", manifest)
        self.assertNotIn("default-flavor", manifest)
        self.assertIn("admissionChecksStrategy:", manifest)
        self.assertIn("name: multikueue-admission-check", manifest)
        self.assertIn("kind: AdmissionCheck", manifest)
        self.assertIn("controllerName: kueue.x-k8s.io/multikueue", manifest)
        self.assertIn("kind: MultiKueueConfig", manifest)
        self.assertIn("kind: MultiKueueCluster", manifest)
        self.assertIn("name: xmc-nightly-worker-1", manifest)
        self.assertIn("clusterSource:", manifest)
        self.assertIn("location: multikueue-worker-kubeconfig", manifest)
        self._assert_no_root_flow_mapping(manifest)

    def test_multikueue_worker_addon_manifest_is_statefulset_without_sidecar(self) -> None:
        manifest = self._read_repo_file(WORKER_MANIFEST_PATH)
        self.assertIn("kind: Service", manifest)
        self.assertIn("kind: StatefulSet", manifest)
        self.assertIn("serviceName: multikueue-worker", manifest)
        self.assertIn("name: multikueue-worker", manifest)
        self.assertIn("rancher/k3s:v1.32.3-k3s1", manifest)
        self.assertIn("name: k3s-server", manifest)
        self.assertIn("--write-kubeconfig=/var/lib/rancher/k3s/k3s.yaml", manifest)
        self.assertIn("volumeClaimTemplates:", manifest)
        self.assertIn("name: k3s-data", manifest)
        self.assertNotIn("bootstrap-sync", manifest)
        self.assertNotIn("sleep infinity", manifest)
        self.assertNotIn("kind: ServiceAccount", manifest)
        self.assertNotIn("kind: Role", manifest)
        self.assertNotIn("kind: RoleBinding", manifest)
        self._assert_no_root_flow_mapping(manifest)

    def test_worker_queues_addon_manifest_defines_priorities_and_queues(self) -> None:
        manifest = self._read_repo_file(WORKER_QUEUES_MANIFEST_PATH)
        self.assertEqual(manifest.count("kind: WorkloadPriorityClass"), 9)
        self.assertIn("kind: ResourceFlavor", manifest)
        self.assertIn("name: cpu-flavor", manifest)
        self.assertIn("kind: ClusterQueue", manifest)
        self.assertIn("name: multikueue-cpu-cluster-queue", manifest)
        self.assertIn("kind: Namespace", manifest)
        self.assertIn("kind: LocalQueue", manifest)
        self.assertIn("name: multikueue-cpu-queue", manifest)
        self._assert_no_root_flow_mapping(manifest)

    def test_setup_multikueue_script_references_addon_manifests(self) -> None:
        script = self._read_repo_file(SETUP_SCRIPT_PATH)
        self.assertIn("multikueue-worker.yaml", script)
        self.assertIn("worker-queues.yaml", script)
        self.assertIn("management-multikueue.yaml", script)
        self.assertIn("xmc.google.com/project=", script)
        self.assertIn("xmc.google.com/location=", script)
        self.assertIn("xmc.google.com/cluster-name=", script)
        self.assertIn("xmc-nightly-worker-1", script)


if __name__ == "__main__":
    unittest.main()
