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

"""Unit tests for tools.dev_env.service_resolver."""

import unittest

from tools.dev_env.service_resolver import (
    resolve_service_name,
    resolve_service_names,
    resolve_target_services,
    service_to_k8s_deployment_name,
)


class ServiceResolverTest(unittest.TestCase):
    """Tests for ServiceResolver utility functions."""

    def test_resolve_service_name_aliases(self) -> None:
        """Verifies common aliases map to canonical names."""
        self.assertEqual(resolve_service_name("ess"), "experiment_state_server")
        self.assertEqual(resolve_service_name("xid"), "xid_service")
        self.assertEqual(resolve_service_name("rod"), "rod_data_provider")
        self.assertEqual(resolve_service_name("gw"), "ingestion_gateway")
        self.assertEqual(resolve_service_name("gateway"), "ingestion_gateway")
        self.assertEqual(resolve_service_name("frontend"), "ui")
        self.assertEqual(resolve_service_name("web"), "ui")
        self.assertEqual(resolve_service_name("dash"), "dashboard_service")
        self.assertEqual(resolve_service_name("dashboard"), "dashboard_service")
        self.assertEqual(resolve_service_name("health"), "health_service")
        self.assertEqual(resolve_service_name("comp"), "components")

    def test_resolve_service_name_canonical_and_kebab(self) -> None:
        """Verifies canonical names and kebab-case strings are preserved or normalized."""
        self.assertEqual(resolve_service_name("actuator"), "actuator")
        self.assertEqual(resolve_service_name("reconciler"), "reconciler")
        self.assertEqual(resolve_service_name("experiment-state-server"), "experiment_state_server")
        self.assertEqual(resolve_service_name("custom-worker"), "custom_worker")

    def test_resolve_service_names_deduplication(self) -> None:
        """Verifies resolving a list of names deduplicates while preserving order."""
        names = ["ess", "ui", "web", "experiment-state-server", "xid", ""]
        resolved = resolve_service_names(names)
        self.assertEqual(resolved, ["experiment_state_server", "ui", "xid_service"])

    def test_service_to_k8s_deployment_name(self) -> None:
        """Verifies mapping from service name or alias to Kubernetes deployment name."""
        self.assertEqual(
            service_to_k8s_deployment_name("ess"), "experiment-state-server-deployment"
        )
        self.assertEqual(service_to_k8s_deployment_name("actuator"), "actuator-deployment")
        self.assertEqual(service_to_k8s_deployment_name("ui"), "ui-deployment")
        self.assertEqual(service_to_k8s_deployment_name("gw"), "gateway-proxy")
        self.assertEqual(service_to_k8s_deployment_name("ingestion_gateway"), "gateway-proxy")
        self.assertEqual(
            service_to_k8s_deployment_name("already-a-deployment"), "already-a-deployment"
        )

    def test_resolve_target_services_explicit(self) -> None:
        """Verifies resolving explicitly specified valid services."""
        enabled = ["experiment_state_server", "xid_service", "ui"]
        targets, helm_needed = resolve_target_services(["ess", "ui"], enabled, helm=False)
        self.assertEqual(targets, {"experiment_state_server", "ui"})
        self.assertFalse(helm_needed)

    def test_resolve_target_services_all(self) -> None:
        """Verifies 'all' keyword resolves to all enabled services."""
        enabled = ["experiment_state_server", "xid_service", "actuator"]
        targets, helm_needed = resolve_target_services(["all"], enabled, helm=False)
        self.assertEqual(targets, {"experiment_state_server", "xid_service", "actuator"})
        self.assertFalse(helm_needed)

    def test_resolve_target_services_helm_keyword_and_flag(self) -> None:
        """Verifies 'helm' keyword and helm=True flag."""
        enabled = ["experiment_state_server"]
        targets, helm_needed = resolve_target_services(["helm"], enabled, helm=False)
        self.assertEqual(targets, set())
        self.assertTrue(helm_needed)

        targets, helm_needed = resolve_target_services(None, enabled, helm=True)
        self.assertEqual(targets, set())
        self.assertTrue(helm_needed)

    def test_resolve_target_services_empty_raises(self) -> None:
        """Verifies empty service list without helm raises ValueError."""
        with self.assertRaises(ValueError) as ctx:
            resolve_target_services([], ["ess"], helm=False)
        self.assertIn("No services specified to reload", str(ctx.exception))

    def test_resolve_target_services_invalid_raises(self) -> None:
        """Verifies disabled or unknown service raises ValueError with enabled list."""
        enabled = ["experiment_state_server", "xid_service"]
        with self.assertRaises(ValueError) as ctx:
            resolve_target_services(["invalid_svc"], enabled, helm=False)
        self.assertIn("invalid or not enabled", str(ctx.exception))
        self.assertIn("experiment_state_server, xid_service", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
