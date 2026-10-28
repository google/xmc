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

"""Helm template tests for the shared mlRun block wired into the ESS and Reconciler ConfigMaps."""

import unittest

from chart_test_lib import render_template, require_helm

ESS_CONFIGMAP = "templates/ess/configmap.yaml"
RECONCILER_CONFIGMAP = "templates/reconciler/configmap.yaml"

BASE_VALUES: dict[str, str] = {
    "gcpServiceAccount": "sa@example.iam.gserviceaccount.com",
    "projectId": "test-project",
}


class MLRunConfigTest(unittest.TestCase):
    """Asserts mlRun values render into the ESS and Reconciler ConfigMaps."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_ess_configmap(self) -> None:
        """ESS ConfigMap renders mlRun values, falling back to chart-level defaults."""
        cases: tuple[tuple[str, dict[str, str], tuple[str, ...]], ...] = (
            (
                "defaults",
                {},
                (
                    'mldiagnostics_environment: "prod"',
                    'mlrun_project: "test-project"',
                    'mlrun_location: "us-central1"',
                    'mlrun_impersonate_sa: ""',
                ),
            ),
            (
                "overrides",
                {
                    "mlRun.environment": "staging",
                    "mlRun.project": "mlrun-project",
                    "mlRun.location": "europe-west4",
                    "mlRun.impersonateServiceAccount": "mlrun@example.iam.gserviceaccount.com",
                },
                (
                    'mldiagnostics_environment: "staging"',
                    'mlrun_project: "mlrun-project"',
                    'mlrun_location: "europe-west4"',
                    'mlrun_impersonate_sa: "mlrun@example.iam.gserviceaccount.com"',
                ),
            ),
            (
                "cluster location fallback",
                {"clusterLocation": "asia-east1"},
                ('mlrun_location: "asia-east1"',),
            ),
            (
                "null mlRun block",
                {"mlRun": "null"},
                ('mldiagnostics_environment: "prod"', 'mlrun_impersonate_sa: ""'),
            ),
            (
                "null reconciler block",
                {"reconciler": "null"},
                ('mldiagnostics_environment: "prod"',),
            ),
        )
        for name, overrides, want_lines in cases:
            with self.subTest(name):
                manifest = render_template(ESS_CONFIGMAP, {**BASE_VALUES, **overrides})
                for line in want_lines:
                    self.assertIn(line, manifest)

    def test_reconciler_configmap(self) -> None:
        """Reconciler ConfigMap renders the environment always and project/location only when set."""
        cases: tuple[tuple[str, dict[str, str], tuple[str, ...], tuple[str, ...]], ...] = (
            (
                "defaults",
                {},
                ('mldiagnostics_environment: "prod"', 'mlrun_impersonate_sa: ""'),
                ("mlrun_project:", "mlrun_location:"),
            ),
            (
                "overrides",
                {
                    "mlRun.environment": "staging",
                    "mlRun.project": "mlrun-project",
                    "mlRun.location": "europe-west4",
                    "mlRun.impersonateServiceAccount": "mlrun@example.iam.gserviceaccount.com",
                },
                (
                    'mldiagnostics_environment: "staging"',
                    'mlrun_project: "mlrun-project"',
                    'mlrun_location: "europe-west4"',
                    'mlrun_impersonate_sa: "mlrun@example.iam.gserviceaccount.com"',
                ),
                (),
            ),
            (
                "null mlRun block",
                {"mlRun": "null"},
                ('mldiagnostics_environment: "prod"', 'mlrun_impersonate_sa: ""'),
                ("mlrun_project:",),
            ),
        )
        for name, overrides, want_lines, absent_lines in cases:
            with self.subTest(name):
                manifest = render_template(RECONCILER_CONFIGMAP, {**BASE_VALUES, **overrides})
                for line in want_lines:
                    self.assertIn(line, manifest)
                for line in absent_lines:
                    self.assertNotIn(line, manifest)


if __name__ == "__main__":
    unittest.main()
