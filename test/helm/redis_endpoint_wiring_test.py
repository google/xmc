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

"""Holds the blueprint's Redis wiring and the redis-endpoint module's interface in step.

The chart reads one value, redisAddr, and three services fail at runtime when it is wrong. Which
Redis that address points at is decided by the redis-endpoint module, because Cluster Toolkit
blueprints have no conditionals and the choice between a provisioned Memorystore instance and an
operator-supplied one cannot be expressed in the blueprint itself.

That indirection is only safe while the blueprint and the module agree on their interface. A
mistyped setting name is accepted by the blueprint parser and surfaces as an unset Terraform
variable much later, so this suite compares the two sides directly rather than waiting for a
`gcluster deploy` to reach the primary group.
"""

import os
import re
import unittest

from chart_test_lib import find_repo_file

BLUEPRINT_PATH = os.path.join("deploy", "ctk_v2", "xmanager-cloud.yaml")
MODULE_DIR = os.path.join("deploy", "ctk_v2", "modules", "redis-endpoint")

MODULE_ID = "redis_endpoint"
MODULE_SOURCE = "./deploy/ctk_v2/modules/redis-endpoint"

# The blueprint is matched with regexes rather than parsed, following the convention in
# workload_identity_binding_test: the chart suites run under the host interpreter in CI, and a
# YAML parser is not among the dependencies that guarantees.
_MODULE_BLOCK = re.compile(
    rf"^  - id: {MODULE_ID}\s*$\n(.*?)(?=^  - id: |\Z)", re.MULTILINE | re.DOTALL
)
_SETTING_KEY = re.compile(r"^      ([a-z_]+):", re.MULTILINE)
_MODULE_REFERENCE = re.compile(rf"\$\({MODULE_ID}\.([a-z_]+)\)")
_TERRAFORM_VARIABLE = re.compile(r'^variable "([a-z_]+)"', re.MULTILINE)
_TERRAFORM_VARIABLE_BLOCK = re.compile(
    r'^variable "([a-z_]+)" \{$\n(.*?)^\}$', re.MULTILINE | re.DOTALL
)
_NULLABLE_FALSE = re.compile(r"^\s*nullable\s*=\s*false\s*$", re.MULTILINE)
_TERRAFORM_OUTPUT = re.compile(r'^output "([a-z_]+)"', re.MULTILINE)
_BLUEPRINT_VAR = re.compile(r"^  ([a-z_]+):", re.MULTILINE)


def read_repo_file(relative_path: str) -> str:
    """Returns the contents of a repository-relative file."""
    with open(find_repo_file(relative_path), encoding="utf-8") as handle:
        return handle.read()


def helm_set_value(blueprint: str, name: str) -> str:
    """Returns the value the blueprint passes to one Helm --set key."""
    match = re.search(
        rf"^      - name: {re.escape(name)}\s*$\n^        value: (.+)$", blueprint, re.MULTILINE
    )
    if not match:
        raise AssertionError(f"the blueprint sets no Helm value named {name}")
    return match.group(1).strip().strip("\"'")


class RedisEndpointWiringTest(unittest.TestCase):
    """Covers the blueprint-to-module agreement behind the address the chart is handed."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.blueprint = read_repo_file(BLUEPRINT_PATH)
        cls.variables_document = read_repo_file(os.path.join(MODULE_DIR, "variables.tf"))
        cls.declared_variables = set(_TERRAFORM_VARIABLE.findall(cls.variables_document))
        cls.declared_outputs = set(
            _TERRAFORM_OUTPUT.findall(read_repo_file(os.path.join(MODULE_DIR, "outputs.tf")))
        )

        block = _MODULE_BLOCK.search(cls.blueprint)
        if not block:
            raise AssertionError(f"the blueprint declares no module with id {MODULE_ID}")
        cls.module_block = block.group(1)
        cls.passed_settings = set(_SETTING_KEY.findall(cls.module_block))
        cls.referenced_outputs = set(_MODULE_REFERENCE.findall(cls.blueprint))

    def test_module_points_at_the_local_source(self) -> None:
        """A stale source path resolves to a missing module only at compile time."""
        self.assertIn(f"source: {MODULE_SOURCE}", self.module_block)

    def test_every_passed_setting_is_a_declared_variable(self) -> None:
        """Terraform rejects an undeclared variable, but not until the primary group applies."""
        self.assertEqual(
            set(),
            self.passed_settings - self.declared_variables,
            f"the blueprint passes these settings to {MODULE_ID}, but {MODULE_DIR}/variables.tf "
            "declares no such variable",
        )

    def test_every_referenced_output_is_a_declared_output(self) -> None:
        self.assertEqual(
            set(),
            self.referenced_outputs - self.declared_outputs,
            f"the blueprint reads these outputs of {MODULE_ID}, but {MODULE_DIR}/outputs.tf "
            "declares no such output",
        )

    def test_module_receives_the_provisioned_instance(self) -> None:
        """Without these the resolver cannot fall back to the instance the blueprint created."""
        self.assertLessEqual(
            {"provisioned_host", "provisioned_port", "provisioned_auth_string"},
            self.passed_settings,
        )

    def test_module_receives_the_external_override(self) -> None:
        self.assertLessEqual(
            {"external_host", "external_port", "external_auth_string"}, self.passed_settings
        )

    def test_the_external_override_variables_reject_null(self) -> None:
        """Null is not "", so it would select the external branch and then resolve to nothing.

        The module reads "" as "not set". A deployment file that names external_redis_host without
        giving it a value passes null instead, which is not equal to "" and so takes the override
        branch, past a precondition that only compares against "". Declaring these non-nullable
        turns that into the default rather than an error deep in the resolver.
        """
        blocks = dict(_TERRAFORM_VARIABLE_BLOCK.findall(self.variables_document))

        for name in ("external_host", "external_port", "external_auth_string"):
            self.assertRegex(blocks[name], _NULLABLE_FALSE, f"{name} accepts null")

    def test_chart_address_comes_from_the_resolver(self) -> None:
        """Reading redis.redis_host directly is what pinned the chart to the provisioned VPC."""
        self.assertEqual(f"$({MODULE_ID}.redis_addr)", helm_set_value(self.blueprint, "redisAddr"))

    def test_chart_password_comes_from_the_resolver(self) -> None:
        """An external host with the provisioned AUTH string authenticates against neither."""
        self.assertEqual(
            f"$({MODULE_ID}.redis_auth_string)", helm_set_value(self.blueprint, "redisPassword")
        )

    def test_override_is_reachable_from_the_deployment_config(self) -> None:
        """A setting wired to vars the overrides file cannot set is unreachable for operators."""
        blueprint_vars = set(_BLUEPRINT_VAR.findall(self.blueprint))

        self.assertLessEqual(
            {"external_redis_host", "external_redis_port", "external_redis_auth_string"},
            blueprint_vars,
        )


if __name__ == "__main__":
    unittest.main()
