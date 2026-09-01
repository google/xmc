# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Holds the greenfield and existing-infrastructure blueprints in step.

Cluster Toolkit blueprints have no conditionals, so installing onto a VPC and GKE cluster that
already exist needs a second file rather than a flag on the first. The two differ only in how
they obtain the network and the cluster; the roughly four hundred lines that wire up Spanner, the
service accounts, certificates and the Helm release are duplicated verbatim, and duplicated
wiring drifts.

This suite pins the differences that are meant to exist and fails on any that are not, so a
change made to one blueprint and forgotten in the other is caught here rather than during an
apply against someone's production cluster. It also checks that the existing-infrastructure
deployment template and its blueprint agree on the variables they name, which `gcluster create`
only reports as a warning.
"""

import os
import re
import unittest

from chart_test_lib import find_repo_file

GREENFIELD_PATH = os.path.join("deploy", "ctk_v2", "xmanager-cloud.yaml")
EXISTING_INFRA_PATH = os.path.join("deploy", "ctk_v2", "xmanager-cloud-existing-infra.yaml")
TEMPLATE_PATH = os.path.join("deploy", "ctk_v2", "xmc-existing-infra-deployment-template.yaml")
GREENFIELD_TEMPLATE_PATH = os.path.join("deploy", "ctk_v2", "xmc-deployment-template.yaml")

# Keys a deployment file carries for the Python tooling rather than for gcluster. They are not
# blueprint variables, so gcluster only warns that they went unused.
TOOLING_ONLY_TEMPLATE_VARS = {"gke_cluster_name"}

# Modules the existing-infrastructure blueprint drops, because it does not create what they
# create, mapped to nothing, and the modules whose source it swaps.
DROPPED_MODULES = {"compute_pool", "node_pool_service_account", "private_service_access"}
SWAPPED_SOURCES = {
    "network": ("modules/network/vpc", "modules/network/pre-existing-vpc"),
    "compute_cluster": (
        "modules/scheduler/gke-cluster",
        "modules/scheduler/pre-existing-gke-cluster",
    ),
}

# Deployment variables that exist in only one of the two blueprints. The greenfield-only ones
# configure the cluster it creates; the existing-infrastructure-only ones name what it looks up.
GREENFIELD_ONLY_VARS = {"gcp_public_cidrs_access_enabled", "authorized_cidr"}
EXISTING_INFRA_ONLY_VARS = {
    "existing_network_name",
    "existing_subnetwork_name",
    "existing_cluster_name",
    "cluster_region",
    "vertical_pod_autoscaler_enabled",
    "redis_connect_mode",
    "redis_reserved_ip_range",
}

# The one Helm value the existing-infrastructure blueprint sets and the greenfield one does not:
# the cluster it installs into may lack the autoscaling.k8s.io CRDs.
EXISTING_INFRA_ONLY_HELM_KEYS = {"verticalPodAutoscaler.enabled"}

# Blueprints and the deployment template are matched with regexes rather than parsed, following
# the convention in workload_identity_binding_test: the chart suites run under the host
# interpreter in CI, and a YAML parser is not among the dependencies that guarantees.
_VARS_BLOCK = re.compile(r"^vars:\s*$\n(.*?)(?=^[a-z_]+:|\Z)", re.MULTILINE | re.DOTALL)
_DECLARED_VAR = re.compile(r"^  ([a-z_0-9]+):", re.MULTILINE)
_REQUIRED_VAR = re.compile(r"^  ([a-z_0-9]+):[ \t]*(?:#.*)?$", re.MULTILINE)
_VAR_REFERENCE = re.compile(r"\$\(vars\.([a-z_0-9]+)\)")
_GROUP = re.compile(r"^- group: ([a-z_0-9]+)", re.MULTILINE)
_MODULE = re.compile(r"^  - id: ([a-z_0-9]+)\s*$\n    source: (\S+)", re.MULTILINE)
_HELM_BLOCK = re.compile(
    r"^  - id: xmc_app_helm\s*$\n(.*?)(?=^  - id: |\Z)", re.MULTILINE | re.DOTALL
)
_HELM_SET_KEY = re.compile(r"^      - name: (\S+)", re.MULTILINE)


def read_repo_file(relative_path: str) -> str:
    """Returns the contents of a repository-relative file."""
    with open(find_repo_file(relative_path), encoding="utf-8") as handle:
        return handle.read()


def declared_vars(document: str) -> set[str]:
    """Returns the deployment variables a blueprint or deployment file declares."""
    return set(_DECLARED_VAR.findall(_VARS_BLOCK.search(document).group(1)))


def required_vars(document: str) -> set[str]:
    """Returns the declared variables left without a value, which the operator must supply."""
    return set(_REQUIRED_VAR.findall(_VARS_BLOCK.search(document).group(1)))


def modules(document: str) -> dict[str, str]:
    """Returns the blueprint's module IDs mapped to their sources."""
    return dict(_MODULE.findall(document))


def helm_set_keys(document: str) -> set[str]:
    """Returns the Helm value names the blueprint's application release sets."""
    return set(_HELM_SET_KEY.findall(_HELM_BLOCK.search(document).group(1)))


class ExistingInfraBlueprintTest(unittest.TestCase):
    """Compares the existing-infrastructure blueprint against its greenfield sibling."""

    @classmethod
    def setUpClass(cls):
        cls.greenfield = read_repo_file(GREENFIELD_PATH)
        cls.existing_infra = read_repo_file(EXISTING_INFRA_PATH)
        cls.template = read_repo_file(TEMPLATE_PATH)

    def test_blueprint_names_differ(self):
        self.assertIn("blueprint_name: xmanager-on-cloud-v2-existing-infra", self.existing_infra)

    def test_deployment_groups_match(self):
        self.assertEqual(_GROUP.findall(self.existing_infra), _GROUP.findall(self.greenfield))

    def test_module_set_differs_only_by_the_dropped_modules(self):
        self.assertEqual(
            set(modules(self.greenfield)) - set(modules(self.existing_infra)), DROPPED_MODULES
        )
        self.assertEqual(set(modules(self.existing_infra)) - set(modules(self.greenfield)), set())

    def test_only_the_network_and_cluster_modules_are_swapped(self):
        greenfield_modules = modules(self.greenfield)
        existing_infra_modules = modules(self.existing_infra)
        swapped = {
            module_id: (greenfield_modules[module_id], source)
            for module_id, source in existing_infra_modules.items()
            if greenfield_modules[module_id] != source
        }
        self.assertEqual(swapped, SWAPPED_SOURCES)

    def test_no_module_creates_a_network_or_a_cluster(self):
        for source in modules(self.existing_infra).values():
            self.assertNotIn(source, ("modules/network/vpc", "modules/scheduler/gke-cluster"))
            self.assertNotEqual(source, "modules/compute/gke-node-pool")

    def test_declared_variables_differ_only_as_documented(self):
        greenfield_vars = declared_vars(self.greenfield)
        existing_infra_vars = declared_vars(self.existing_infra)
        self.assertEqual(greenfield_vars - existing_infra_vars, GREENFIELD_ONLY_VARS)
        self.assertEqual(existing_infra_vars - greenfield_vars, EXISTING_INFRA_ONLY_VARS)

    def test_every_referenced_variable_is_declared(self):
        referenced = set(_VAR_REFERENCE.findall(self.existing_infra))
        self.assertTrue(referenced, "found no $(vars.*) references, the regex is wrong")
        self.assertEqual(referenced - declared_vars(self.existing_infra), set())

    def test_dropped_variables_are_referenced_nowhere(self):
        for name in GREENFIELD_ONLY_VARS:
            self.assertNotIn(f"$(vars.{name})", self.existing_infra)

    def test_helm_values_differ_only_by_the_autoscaler_toggle(self):
        greenfield_keys = helm_set_keys(self.greenfield)
        existing_infra_keys = helm_set_keys(self.existing_infra)
        self.assertEqual(greenfield_keys - existing_infra_keys, set())
        self.assertEqual(existing_infra_keys - greenfield_keys, EXISTING_INFRA_ONLY_HELM_KEYS)

    def test_autoscaler_toggle_is_wired_to_its_variable(self):
        self.assertIn(
            "- name: verticalPodAutoscaler.enabled\n"
            "        value: $(vars.vertical_pod_autoscaler_enabled)",
            self.existing_infra,
        )

    def test_nothing_manages_the_networks_service_networking_connection(self):
        # The module writes its range as the connection's only reserved range, and a VPC has just
        # one connection, so running it against a network that already peers with
        # servicenetworking would strip the ranges other services on that network are using.
        psa = "modules/network/private-service-access"
        self.assertNotIn(psa, set(modules(self.existing_infra).values()))
        self.assertIn(psa, set(modules(self.greenfield).values()))

    def test_memorystore_peers_without_touching_private_service_access(self):
        self.assertIn("connect_mode: $(vars.redis_connect_mode)", self.existing_infra)
        self.assertIn("reserved_ip_range: $(vars.redis_reserved_ip_range)", self.existing_infra)
        self.assertIn("redis_connect_mode: DIRECT_PEERING", self.existing_infra)

    def test_template_declares_nothing_the_blueprint_does_not(self):
        extra = declared_vars(self.template) - declared_vars(self.existing_infra)
        self.assertEqual(extra - TOOLING_ONLY_TEMPLATE_VARS, set())

    def test_the_tooling_only_exemption_matches_the_greenfield_pair(self):
        # Keeps TOOLING_ONLY_TEMPLATE_VARS from growing into a place to hide typos: a key is
        # exempt here only while the greenfield template carries it outside its blueprint too.
        greenfield_template = read_repo_file(GREENFIELD_TEMPLATE_PATH)
        extra = declared_vars(greenfield_template) - declared_vars(self.greenfield)
        self.assertEqual(extra, TOOLING_ONLY_TEMPLATE_VARS)

    def test_template_supplies_every_variable_the_blueprint_leaves_unset(self):
        self.assertEqual(required_vars(self.existing_infra) - declared_vars(self.template), set())

    def test_template_names_the_cluster_for_the_tooling_as_well(self):
        # gke_cluster_name is what get-credentials and the Gateway API check look the cluster up
        # by; left unset it derives a name the greenfield blueprint would have created.
        self.assertIn("gke_cluster_name:", self.template)
        self.assertNotIn('gke_cluster_name: ""', self.template)


if __name__ == "__main__":
    unittest.main()
