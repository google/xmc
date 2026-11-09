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

"""Covers the plumbing every chart suite depends on to report honest results."""

import os
import unittest
from unittest import mock

from chart_test_lib import (
    find_chart_dir,
    find_repo_file,
    render_chart,
    render_template,
    require_helm,
    skip_unless_required,
)


class SkipUnlessRequiredTest(unittest.TestCase):
    """Covers the guard that stops a missing helm binary from silently greening a CI run."""

    def test_skips_when_prerequisites_are_optional(self) -> None:
        with mock.patch.dict(os.environ):
            os.environ.pop("XMC_REQUIRE_HELM", None)

            with self.assertRaises(unittest.SkipTest):
                skip_unless_required("helm binary not available")

    def test_raises_when_prerequisites_are_required(self) -> None:
        # A workflow may spell the flag as a YAML boolean, which reaches the process as "true", so
        # every common truthy spelling has to arm the guard rather than silently skip the suite.
        for value in ("1", "true", "TRUE", "yes", " True "):
            with self.subTest(value=value):
                with mock.patch.dict(os.environ, {"XMC_REQUIRE_HELM": value}):
                    with self.assertRaises(RuntimeError):
                        skip_unless_required("helm binary not available")

    def test_skips_when_the_flag_is_not_truthy(self) -> None:
        for value in ("", "0", "false", "no"):
            with self.subTest(value=value):
                with mock.patch.dict(os.environ, {"XMC_REQUIRE_HELM": value}):
                    with self.assertRaises(unittest.SkipTest):
                        skip_unless_required("helm binary not available")


class RenderTemplateTest(unittest.TestCase):
    """Covers chart discovery and the rendering wrapper the assertions are written against."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_chart_dir_is_discoverable(self) -> None:
        self.assertTrue(os.path.isfile(os.path.join(find_chart_dir(), "Chart.yaml")))

    def test_repo_file_is_discoverable(self) -> None:
        """Suites that compare the chart against another file need to find that file too."""
        located = find_repo_file(os.path.join("deploy", "helm", "values.yaml"))

        self.assertTrue(os.path.isfile(located))

    def test_missing_repo_file_does_not_pass_silently(self) -> None:
        """A packaging error must fail whether or not the caller declared helm is required."""
        with self.assertRaises(FileNotFoundError):
            find_repo_file(os.path.join("deploy", "does-not-exist.yaml"))

    def test_render_returns_the_requested_manifest(self) -> None:
        manifest = render_template(
            "templates/dashboard-service/deployment.yaml",
            {"dashboardServiceDeployment.enabled": "true"},
        )

        self.assertIn("kind: Deployment", manifest)

    def test_render_passes_release_namespace_when_requested(self) -> None:
        manifest = render_template(
            "templates/components/rbac.yaml",
            {},
            namespace="custom-ns",
        )

        self.assertIn("xmc-bridge-cluster-identity-custom-ns", manifest)

    def test_render_chart_returns_every_manifest(self) -> None:
        """Suites whose subject is the set of rendered resources render without --show-only."""
        manifests = render_chart({"dashboardServiceDeployment.enabled": "true"})

        self.assertIn("templates/dashboard-service/deployment.yaml", manifests)
        self.assertIn("templates/ess/deployment.yaml", manifests)

    def test_render_surfaces_the_template_error(self) -> None:
        """A bare CalledProcessError hides the helm output that says what went wrong."""
        with self.assertRaises(RuntimeError) as raised:
            render_template("templates/does-not-exist.yaml", {})

        self.assertIn("templates/does-not-exist.yaml", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
