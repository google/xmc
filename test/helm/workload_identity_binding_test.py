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

"""Holds the chart's Workload Identity annotations and the blueprint's bindings in step.

Annotating a Kubernetes service account with iam.gke.io/gcp-service-account only declares which
Google service account its pods want to act as. The pods can actually mint tokens for it once the
blueprint also grants roles/iam.workloadIdentityUser on that Google account, and the two live in
different files. The dashboard service shipped with the annotation and without the binding, so its
calls reached Google with no identity attached and came back PermissionDenied at runtime. Nothing
in either file makes that omission visible, which is what this suite is for.
"""

import os
import re
import unittest

from chart_test_lib import find_repo_file, render_chart, require_helm

BLUEPRINT_PATH = os.path.join("deploy", "ctk_v2", "xmanager-cloud.yaml")

# Every deployment the chart gates behind a toggle, turned on together: a service account that only
# renders for one release still needs its binding, so the suite has to see all of them at once. The
# Google service account emails are placeholders; only the presence of the annotation matters here.
ALL_SERVICES_ENABLED = {
    "gcpServiceAccount": "xmc-wl-sa@example.iam.gserviceaccount.com",
    "componentsDeployment.enabled": "true",
    "dashboardServiceDeployment.enabled": "true",
    "dashboardService.gcpServiceAccount": "xmc-wl-sa@example.iam.gserviceaccount.com",
    "database.backend": "postgres",
    "database.experimentalPostgres": "true",
    "database.postgres.deploy": "true",
    "healthServiceDeployment.enabled": "true",
    "healthService.gcpServiceAccount": "xmc-hs-sa@example.iam.gserviceaccount.com",
    "ingestionGatewayDeployment.enabled": "true",
    "logMonitorDeployment.enabled": "true",
    "logMonitor.gcpServiceAccount": "xmc-lm-sa@example.iam.gserviceaccount.com",
    "rodDeployment.enabled": "true",
    "ui.enabled": "true",
}

# Manifests are matched with regexes rather than parsed, because the chart suites run under the
# host interpreter in CI and a YAML parser is not among the dependencies that guarantees.
_SERVICE_ACCOUNT_KIND = re.compile(r"^kind:\s*ServiceAccount\s*$", re.MULTILINE)
_WORKLOAD_IDENTITY_ANNOTATION = re.compile(
    r"^\s+iam\.gke\.io/gcp-service-account:\s*\S", re.MULTILINE
)
# Stopping at the first space or '#' rather than anchoring to end-of-line: a trailing inline
# comment would make an anchored pattern fail the whole line, dropping that account from the set
# and turning a real gap into a vacuous pass. The blueprint the bound names come from is
# hand-written, so a comment beside a value is entirely plausible there.
_METADATA_NAME = re.compile(r"^\s{2}name:\s*([^\s#]+)", re.MULTILINE)
_BOUND_NAME = re.compile(r"^\s*k8s_service_account_name:\s*([^\s#]+)", re.MULTILINE)


def annotated_service_accounts(manifests: str) -> set[str]:
    """Returns the names of the rendered service accounts that ask to act as a Google account."""
    names = set()
    # Trailing whitespace after a separator is legal YAML and would silently merge two documents
    # into one, which would then be searched for a name that belongs to the wrong resource.
    for document in re.split(r"^---\s*$", manifests, flags=re.MULTILINE):
        if not _SERVICE_ACCOUNT_KIND.search(document):
            continue
        if not _WORKLOAD_IDENTITY_ANNOTATION.search(document):
            continue
        name = _METADATA_NAME.search(document)
        if name:
            names.add(name.group(1).strip("\"'"))
    return names


def bound_service_accounts(blueprint: str) -> set[str]:
    """Returns the Kubernetes service account names the blueprint declares bindings for."""
    return {match.strip("\"'") for match in _BOUND_NAME.findall(blueprint)}


class WorkloadIdentityBindingTest(unittest.TestCase):
    """Covers the chart-to-blueprint agreement that gives annotated pods a usable identity."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()
        with open(find_repo_file(BLUEPRINT_PATH), encoding="utf-8") as blueprint:
            cls.bound = bound_service_accounts(blueprint.read())
        cls.annotated = annotated_service_accounts(render_chart(ALL_SERVICES_ENABLED))

    def test_the_chart_annotates_service_accounts(self) -> None:
        """Guards the two set comparisons below from passing on an empty rendering."""
        self.assertIn("experiment-server", self.annotated)
        self.assertIn("dashboard-service-account", self.annotated)
        self.assertIn("xmc-bridge-sa", self.annotated)

    def test_every_annotated_service_account_is_bound(self) -> None:
        """An annotation without a binding is what made the dashboard service unauthenticated."""
        self.assertEqual(
            set(),
            self.annotated - self.bound,
            "these service accounts are annotated in deploy/helm but have no "
            f"workload_identity_binding in {BLUEPRINT_PATH}",
        )

    def test_every_binding_names_an_annotated_service_account(self) -> None:
        """A binding whose service account no longer exists is dead IAM grant on the project."""
        self.assertEqual(
            set(),
            self.bound - self.annotated,
            f"{BLUEPRINT_PATH} binds these service accounts, but no template in deploy/helm "
            "renders a service account by that name carrying the annotation",
        )


if __name__ == "__main__":
    unittest.main()
