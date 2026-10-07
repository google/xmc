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

"""Tests that tools/run_affected_tests.py mirrors the CI unit test configuration."""

import re
import shlex
import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import ci_config_reader, run_affected_tests  # noqa: E402

# Under Bazel the CI files are data files in the runfiles tree of this test, so the path is
# made absolute without resolving symlinks.
CI_FILES_ROOT = Path(__file__).absolute().parent.parent
BACKEND_ACTION, UI_ACTION, CHART_ACTION = "run-backend-tests", "run-ui-tests", "run-chart-tests"

# The role of every step of the CI unit test actions: scope steps decide what runs, CI-only
# steps prepare the runner, gated steps run the tests that the tool runs or reports, and skip
# steps log why the tests did not run. An unclassified step fails the test, so that a new CI
# step gets reviewed for parity.
SCOPE, CI_ONLY, GATED, SKIP = "scope", "ci_only", "gated", "skip"
STEP_ROLES: dict[str, dict[str, str]] = {
    BACKEND_ACTION: {
        "Classify Backend Test Requirement": SCOPE,
        "Set up Bazel": CI_ONLY,
        "Restore Bazel Repository Cache": CI_ONLY,
        "Resolve Backend Test Scope": SCOPE,
        # The tool checks the emulator with its own pre-flight instead.
        "Verify Spanner Emulator Endpoint": CI_ONLY,
        "Run Backend Unit Tests": GATED,
        "Save Bazel Repository Cache": CI_ONLY,
        "Log Backend Test Skip Summary": SKIP,
    },
    UI_ACTION: {
        "Resolve UI Test Scope": SCOPE,
        "Set up Node.js": CI_ONLY,
        "Install Node.js dependencies": GATED,
        "Run Angular UI Unit Tests": GATED,
        "Install Playwright Browsers": GATED,
        "Build Angular Application": GATED,
        "Run Playwright Visual Screenshot Tests": GATED,
        "Log UI Test Skip Summary": SKIP,
    },
    CHART_ACTION: {
        "Check Changed Chart Paths": SCOPE,
        "Install Helm": CI_ONLY,
        "Run Helm Lint": GATED,
        "Run Helm Chart Configuration Tests": GATED,
        "Log Chart Test Skip Summary": SKIP,
    },
}
# The condition of the gated steps of each action.
SUITE_CONDITIONS = {
    BACKEND_ACTION: "steps.scope.outputs.run_backend == 'true'",
    UI_ACTION: "steps.scope.outputs.run_ui == 'true'",
    CHART_ACTION: "steps.changes.outputs.chart == 'true'",
}
RESOLVER_COMMAND = "python3 tools/benchmark/resolve_test_scope.py"
# The commands of the scope steps. The tool runs the same resolver on the changed files, so
# a new resolver flag in CI needs a review for parity; the chart scope step is a paths filter.
SCOPE_COMMANDS: dict[str, list[str]] = {
    "Classify Backend Test Requirement": [f"{RESOLVER_COMMAND} --no-rdeps"],
    "Resolve Backend Test Scope": [RESOLVER_COMMAND],
    "Resolve UI Test Scope": [f"{RESOLVER_COMMAND} --no-rdeps"],
    "Check Changed Chart Paths": [],
}
# The with: inputs that the unit test workflow passes to each action, overriding its defaults.
WORKFLOW_INPUTS: dict[str, dict[str, str]] = {BACKEND_ACTION: {}, UI_ACTION: {}, CHART_ACTION: {}}
# Flags of the CI `bazel test` step that set up CI caching rather than the tests.
CI_ONLY_BAZEL_FLAGS = frozenset(
    {
        "--config",
        "--remote_upload_local_results",
        "--repository_cache",
        "--experimental_repository_cache_hardlinks",
    }
)
# Commands of gated steps that only inspect the runner, such as logging the Helm version.
CI_ONLY_COMMANDS = frozenset({"helm version --short"})
# The only flags that `bazel test --config=ci` may take from .bazelrc, compared by name:
# remote caching and parallelism do not change test results, so the tool runs `bazel test`
# without --config=ci.
CI_CONFIG_FLAGS = frozenset(
    {
        "--remote_cache",
        "--remote_upload_local_results",
        "--remote_download_toplevel",
        "--experimental_remote_cache_compression",
        "--jobs",
    }
)


def read_ci_file(relative_path: str) -> str:
    """Returns the text of a CI file, given by its path from the repository root."""
    return (CI_FILES_ROOT / relative_path).read_text(encoding="utf-8")


def read_action(action: str) -> str:
    """Returns the text of .github/actions/<action>/action.yml."""
    return read_ci_file(f".github/actions/{action}/action.yml")


def steps_with_role(action: str, role: str) -> dict[str, str]:
    """Maps the names of the action steps that STEP_ROLES gives role to their text."""
    steps = ci_config_reader.action_steps(read_action(action))
    return {name: step for name, step in steps.items() if STEP_ROLES[action].get(name) == role}


def action_inputs(action: str) -> dict[str, str]:
    """Returns the inputs that CI runs action with: its defaults, then WORKFLOW_INPUTS."""
    defaults = ci_config_reader.action_input_defaults(read_action(action))
    return {**defaults, **WORKFLOW_INPUTS[action]}


def gated_invocations(action: str) -> list[tuple[str, str]]:
    """Returns the (working directory, command) pairs of the gated steps of action, in order.

    Commands are prefixed with their step env, inputs take their action_inputs values, and
    CI_ONLY_COMMANDS are left out.
    """
    inputs = action_inputs(action)
    pairs: list[tuple[str, str]] = []
    for step in steps_with_role(action, GATED).values():
        env_vars = ci_config_reader.step_env(step, inputs).items()
        env = "".join(f"{name}={value} " for name, value in env_vars)
        directory = ci_config_reader.step_working_directory(step, inputs)
        commands = (c for c in ci_config_reader.step_commands(step) if c not in CI_ONLY_COMMANDS)
        pairs.extend((directory, env + command) for command in commands)
    return pairs


def backend_bazel_test_args() -> list[str]:
    """Returns the arguments after `bazel test` in the CI backend step, step env expanded."""
    step = ci_config_reader.action_steps(read_action(BACKEND_ACTION))["Run Backend Unit Tests"]
    env = ci_config_reader.step_env(step, action_inputs(BACKEND_ACTION))
    command = next(c for c in ci_config_reader.step_commands(step) if c.startswith("bazel test "))
    return shlex.split(ci_config_reader.expand_env_references(command, env))[2:]


class CiParityTest(unittest.TestCase):
    """Tests that the CI behavior the tool runs or reports matches the CI files."""

    def assert_matches_ci(self, suite: run_affected_tests.OtherSuite, action: str) -> None:
        """Asserts that suite has the commands and working directory CI runs it with."""
        expected = [(suite.working_directory, command) for command in suite.commands]
        self.assertEqual(gated_invocations(action), expected)

    def test_every_action_step_is_classified(self) -> None:
        for action, roles in STEP_ROLES.items():
            with self.subTest(action=action):
                steps = ci_config_reader.action_steps(read_action(action))
                self.assertCountEqual(list(steps), list(roles))

    def test_gated_steps_run_only_when_their_suite_is_selected(self) -> None:
        for action, condition in SUITE_CONDITIONS.items():
            for name, step in steps_with_role(action, GATED).items():
                with self.subTest(step=name):
                    self.assertEqual(ci_config_reader.step_condition(step), condition)

    def test_scope_steps_run_the_pinned_commands(self) -> None:
        commands = {
            name: ci_config_reader.step_commands(step)
            for action in STEP_ROLES
            for name, step in steps_with_role(action, SCOPE).items()
        }
        self.assertEqual(commands, SCOPE_COMMANDS)

    def test_skip_steps_only_log(self) -> None:
        for action in STEP_ROLES:
            for name, step in steps_with_role(action, SKIP).items():
                with self.subTest(step=name):
                    programs = {c.split()[0] for c in ci_config_reader.step_commands(step)}
                    self.assertEqual(programs, {"echo"})

    def test_bazel_test_flags_match_the_backend_action(self) -> None:
        args = backend_bazel_test_args()
        ci_flags = args[: args.index("--")]
        flags = tuple(f for f in ci_flags if f.partition("=")[0] not in CI_ONLY_BAZEL_FLAGS)
        self.assertEqual(flags, run_affected_tests.BAZEL_TEST_FLAGS)

    def test_full_suite_is_the_backend_action_default_targets(self) -> None:
        defaults = re.findall(r"\$\{TARGETS:-([^}]*)\}", read_action(BACKEND_ACTION))
        self.assertEqual([tuple(d.split()) for d in defaults], [run_affected_tests.FULL_SUITE])

    def test_ui_suite_matches_the_ui_action(self) -> None:
        self.assert_matches_ci(run_affected_tests.UI_SUITE, UI_ACTION)

    def test_helm_suite_matches_the_chart_action(self) -> None:
        self.assert_matches_ci(run_affected_tests.HELM_SUITE, CHART_ACTION)

    def test_helm_path_patterns_match_the_chart_action(self) -> None:
        patterns = ci_config_reader.paths_filter_patterns(read_action(CHART_ACTION), "chart")
        self.assertEqual(tuple(patterns), run_affected_tests.HELM_SUITE_PATH_PATTERNS)

    def test_emulator_image_matches_the_unit_test_workflow(self) -> None:
        workflow = read_ci_file(".github/workflows/unit-tests.yml")
        images = ci_config_reader.container_images(workflow)
        self.assertIn(run_affected_tests.EMULATOR_IMAGE, images)

    def test_workflow_uses_each_action_once_with_the_pinned_inputs(self) -> None:
        workflow = read_ci_file(".github/workflows/unit-tests.yml")
        for action, inputs in WORKFLOW_INPUTS.items():
            with self.subTest(action=action):
                path = f"./.github/actions/{action}"
                self.assertEqual(ci_config_reader.workflow_step_inputs(workflow, path), [inputs])

    def test_ci_config_only_sets_remote_cache_and_jobs_flags(self) -> None:
        names = set(ci_config_reader.config_flag_names(read_ci_file(".bazelrc"), "ci"))
        self.assertTrue(names)
        self.assertEqual(names - CI_CONFIG_FLAGS, set())


if __name__ == "__main__":
    unittest.main()
