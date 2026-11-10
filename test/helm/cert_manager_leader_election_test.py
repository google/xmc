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

"""Verifies that the blueprint keeps cert-manager leader election leases out of kube-system.

By default, cert-manager-cainjector creates its leader election lease in kube-system. On GKE
Autopilot, GKE Warden enforces managed-namespaces-limitation and blocks lease creation in
kube-system, preventing cainjector from injecting caBundle into webhook configurations.
"""

import os
import re
import unittest

from chart_test_lib import find_repo_file

BLUEPRINT_PATH = os.path.join("deploy", "ctk_v2", "xmanager-cloud.yaml")
_MODULE_HEADER = re.compile(r"^\s*-\s+id:\s*([^\s#]+)", re.MULTILINE)
_GROUP_HEADER = re.compile(r"^-\s+group:\s*([^\s#]+)", re.MULTILINE)
_NAMESPACE_LINE = re.compile(r"^\s+namespace:\s*([^\s#]+)", re.MULTILINE)
_SET_VALUE_PAIR = re.compile(
    r"^\s*-\s+name:\s*([^\s#]+)\s*\n(?:\s*#.*\n)*\s+value:\s*([^\s#]+)",
    re.MULTILINE,
)


def extract_module_block(blueprint: str, module_id: str) -> str:
    """Returns the raw YAML slice for the given blueprint module id."""
    for match in _MODULE_HEADER.finditer(blueprint):
        if match.group(1).strip("\"'") != module_id:
            continue
        start = match.end()
        next_mod = _MODULE_HEADER.search(blueprint, start)
        next_grp = _GROUP_HEADER.search(blueprint, start)
        candidates = [m.start() for m in (next_mod, next_grp) if m is not None]
        end = min(candidates) if candidates else len(blueprint)
        return blueprint[match.start() : end]
    raise AssertionError(f"module {module_id!r} not found in {BLUEPRINT_PATH}")


def parse_set_values(module_block: str) -> dict[str, str]:
    """Extracts name/value pairs from a direct-helm-install module's set_values block."""
    return {
        name.strip("\"'"): value.strip("\"'")
        for name, value in _SET_VALUE_PAIR.findall(module_block)
    }


class CertManagerLeaderElectionTest(unittest.TestCase):
    """Checks that install_cert_manager pins global.leaderElection.namespace to cert-manager."""

    def test_leader_election_namespace_matches_release_namespace(self) -> None:
        """Prevents GKE Autopilot Warden from rejecting cainjector leases in kube-system."""
        with open(find_repo_file(BLUEPRINT_PATH), encoding="utf-8") as blueprint_file:
            block = extract_module_block(blueprint_file.read(), "install_cert_manager")
        ns_match = _NAMESPACE_LINE.search(block)
        if ns_match is None:
            self.fail("namespace setting not found on install_cert_manager")
        release_namespace = ns_match.group(1).strip("\"'")
        set_values = parse_set_values(block)
        self.assertEqual("cert-manager", release_namespace)
        self.assertEqual(
            release_namespace,
            set_values.get("global.leaderElection.namespace"),
        )

    def test_extract_module_block_and_parse_set_values_edge_cases(self) -> None:
        """Covers comments, quotes, multibyte values, and missing modules."""
        sample = (
            "- group: prereqs\n"
            "  modules:\n"
            "  - id: mod_a # comment\n"
            "    settings:\n"
            "      set_values:\n"
            "      - name: 'key.one'\n"
            "        # inline comment with multibyte — ✓\n"
            '        value: "val-one"\n'
            "  - id: mod_b\n"
            "    settings:\n"
            "      set_values:\n"
            "      - name: other.key\n"
            "        value: other-val\n"
        )
        block_a = extract_module_block(sample, "mod_a")
        self.assertEqual({"key.one": "val-one"}, parse_set_values(block_a))
        self.assertEqual({}, parse_set_values(""))
        with self.assertRaises(AssertionError):
            extract_module_block(sample, "missing_mod")


if __name__ == "__main__":
    unittest.main()
