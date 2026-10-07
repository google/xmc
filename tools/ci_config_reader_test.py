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

"""Unit tests for the CI configuration reader, on fixtures in the layout of the CI files."""

import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import ci_config_reader  # noqa: E402

ACTION = """\
name: 'Fixture Action'

inputs:
  directory:
    description: 'Where the tests run'
    required: false
    default: 'cmd/app'
  level:
    default: "2"  # Double quotes work too.

runs:
  using: 'composite'
  steps:
    - name: 'Check Changed Paths'
      id: 'changes'
      uses: 'dorny/paths-filter@v3'
      with:
        filters: |
          docs:
            - 'docs/**'
          app:
            # Comments between patterns are skipped.
            - 'cmd/app/**'
            - "go.mod"

    - name: 'Run Tests'
      if: steps.changes.outputs.app == 'true'
      env:
        LEVEL: ${{ inputs.level }}
        REF: ${{ github.ref }}
      shell: bash
      run: |
        make test \\
          --level="${LEVEL}"
        make lint
      working-directory: ${{ inputs.directory }}
"""
INPUTS = {"directory": "cmd/app", "level": "2"}
WORKFLOW = """\
jobs:
  tests:
    container:
      image: 'docker.io/library/builder:3'
    services:
      database:
        image: gcr.io/example/database:1.2.3
    steps:
      - name: 'Checkout'
        uses: 'actions/checkout@v4'  # A pinned commit in CI.
        with:
          fetch-depth: 0
      - name: 'Run Tests'
        uses: './.github/actions/run-tests'
        with:
          level: "3"
  lint:
    steps:
      - uses: './.github/actions/run-lint'
"""
BAZELRC = """\
# build:ci --commented_out
build --stamp
build:ci_extra --other_config
query:ci --output=label
build:ci --remote_cache=grpc://cache.example:9092
test:ci --jobs=14 --test_env=TZ  # Trailing comment.
"""


def fixture_step(name: str) -> str:
    """Returns the text of the ACTION step called name."""
    return ci_config_reader.action_steps(ACTION)[name]


class ScalarReaderTest(unittest.TestCase):
    """Tests for reading single-line YAML scalars."""

    def test_yaml_scalar_unescapes_single_quotes(self) -> None:
        self.assertEqual(ci_config_reader.yaml_scalar("'it''s'"), "it's")

    def test_yaml_scalar_unescapes_double_quotes(self) -> None:
        self.assertEqual(ci_config_reader.yaml_scalar(r'"say \"hi\""'), 'say "hi"')

    def test_yaml_scalar_drops_a_trailing_comment(self) -> None:
        self.assertEqual(ci_config_reader.yaml_scalar("make test  # Runs tests."), "make test")

    def test_yaml_scalar_rejects_multiline_text(self) -> None:
        with self.assertRaises(ValueError):
            ci_config_reader.yaml_scalar("make\ntest")


class ActionReaderTest(unittest.TestCase):
    """Tests for reading steps, inputs, and paths filters from an action.yml."""

    def test_action_steps_maps_unquoted_names_in_order(self) -> None:
        steps = ci_config_reader.action_steps(ACTION)
        self.assertEqual(list(steps), ["Check Changed Paths", "Run Tests"])

    def test_step_key_lines_returns_an_inline_value(self) -> None:
        lines = ci_config_reader.step_key_lines(fixture_step("Run Tests"), "if")
        self.assertEqual(lines, ["steps.changes.outputs.app == 'true'"])

    def test_step_key_lines_unquote_an_inline_value(self) -> None:
        step = '      working-directory: "cmd/app"  # Comment.\n'
        self.assertEqual(ci_config_reader.step_key_lines(step, "working-directory"), ["cmd/app"])

    def test_step_key_lines_returns_stripped_block_lines(self) -> None:
        lines = ci_config_reader.step_key_lines(fixture_step("Run Tests"), "env")
        self.assertEqual(lines, ["LEVEL: ${{ inputs.level }}", "REF: ${{ github.ref }}"])

    def test_step_key_lines_read_a_block_with_a_chomping_indicator(self) -> None:
        step = "      run: |-\n        make style\n"
        self.assertEqual(ci_config_reader.step_key_lines(step, "run"), ["make style"])

    def test_step_key_lines_skip_comment_lines_in_a_block(self) -> None:
        step = "      run: |\n        # Formats the code.\n        make style\n"
        self.assertEqual(ci_config_reader.step_key_lines(step, "run"), ["make style"])

    def test_step_key_lines_ignores_nested_keys(self) -> None:
        step = fixture_step("Check Changed Paths")
        self.assertEqual(ci_config_reader.step_key_lines(step, "filters"), [])

    def test_step_key_lines_of_an_unset_key_is_empty(self) -> None:
        self.assertEqual(ci_config_reader.step_key_lines(fixture_step("Run Tests"), "uses"), [])

    def test_step_condition_reads_the_expression_with_or_without_a_wrapper(self) -> None:
        for condition in ("${{ github.ref == 'main' }}", "github.ref == 'main'"):
            with self.subTest(condition=condition):
                step = f"      if: {condition}\n"
                self.assertEqual(ci_config_reader.step_condition(step), "github.ref == 'main'")

    def test_action_input_defaults_unquote_defaults_and_skip_other_keys(self) -> None:
        self.assertEqual(ci_config_reader.action_input_defaults(ACTION), INPUTS)

    def test_action_input_defaults_name_an_input_without_a_quoted_default(self) -> None:
        for definition in ("    required: true\n", "    default: 2\n"):
            action = f"name: 'Fixture'\ninputs:\n  level:\n{definition}\nruns:\n"
            with self.subTest(definition=definition), self.assertRaisesRegex(ValueError, "'level'"):
                ci_config_reader.action_input_defaults(action)

    def test_substitute_inputs_keeps_other_expressions(self) -> None:
        text = ci_config_reader.substitute_inputs("${{ inputs.level }} ${{ github.ref }}", INPUTS)
        self.assertEqual(text, "2 ${{ github.ref }}")

    def test_substitute_inputs_rejects_an_undefined_input(self) -> None:
        with self.assertRaisesRegex(ValueError, "'missing'"):
            ci_config_reader.substitute_inputs("${{ inputs.missing }}", INPUTS)

    def test_step_env_substitutes_inputs(self) -> None:
        env = ci_config_reader.step_env(fixture_step("Run Tests"), INPUTS)
        self.assertEqual(env, {"LEVEL": "2", "REF": "${{ github.ref }}"})

    def test_step_env_unquotes_values(self) -> None:
        step = "      env:\n        MODE: 'strict'\n        TARGET: \"./...\"\n"
        env = ci_config_reader.step_env(step, {})
        self.assertEqual(env, {"MODE": "strict", "TARGET": "./..."})

    def test_step_working_directory_substitutes_inputs(self) -> None:
        directory = ci_config_reader.step_working_directory(fixture_step("Run Tests"), INPUTS)
        self.assertEqual(directory, "cmd/app")

    def test_step_working_directory_defaults_to_the_repository_root(self) -> None:
        step = fixture_step("Check Changed Paths")
        self.assertEqual(ci_config_reader.step_working_directory(step, INPUTS), ".")

    def test_step_commands_join_line_continuations(self) -> None:
        commands = ci_config_reader.step_commands(fixture_step("Run Tests"))
        self.assertEqual(commands, ['make test --level="${LEVEL}"', "make lint"])

    def test_expand_env_references_replaces_only_defined_names(self) -> None:
        command = ci_config_reader.expand_env_references("--level=${LEVEL} ${HOME}", {"LEVEL": "2"})
        self.assertEqual(command, "--level=2 ${HOME}")

    def test_paths_filter_patterns_skip_comments_and_quotes(self) -> None:
        patterns = ci_config_reader.paths_filter_patterns(ACTION, "app")
        self.assertEqual(patterns, ["cmd/app/**", "go.mod"])

    def test_paths_filter_patterns_skip_blank_lines(self) -> None:
        action = "        app:\n          - 'a/**'\n\n          - 'b/**'\n\n    - name: 'Next'\n"
        self.assertEqual(ci_config_reader.paths_filter_patterns(action, "app"), ["a/**", "b/**"])

    def test_paths_filter_patterns_read_items_level_with_the_filter(self) -> None:
        action = "        app:\n        - 'a/**'\n        docs:\n        - 'docs/**'\n"
        self.assertEqual(ci_config_reader.paths_filter_patterns(action, "app"), ["a/**"])

    def test_paths_filter_patterns_stop_at_the_next_filter(self) -> None:
        self.assertEqual(ci_config_reader.paths_filter_patterns(ACTION, "docs"), ["docs/**"])


class WorkflowReaderTest(unittest.TestCase):
    """Tests for reading container images and step inputs from a workflow file."""

    def test_container_images_are_unquoted_and_in_order(self) -> None:
        images = ci_config_reader.container_images(WORKFLOW)
        self.assertEqual(images, ["docker.io/library/builder:3", "gcr.io/example/database:1.2.3"])

    def test_workflow_step_inputs_read_the_with_entries_of_the_action_step(self) -> None:
        inputs = ci_config_reader.workflow_step_inputs(WORKFLOW, "./.github/actions/run-tests")
        self.assertEqual(inputs, [{"level": "3"}])

    def test_workflow_step_inputs_of_a_step_without_with_are_empty(self) -> None:
        inputs = ci_config_reader.workflow_step_inputs(WORKFLOW, "./.github/actions/run-lint")
        self.assertEqual(inputs, [{}])

    def test_workflow_step_inputs_of_an_unused_action_are_empty(self) -> None:
        self.assertEqual(ci_config_reader.workflow_step_inputs(WORKFLOW, "./.github/actions/x"), [])


class BazelrcReaderTest(unittest.TestCase):
    """Tests for reading the flags of a .bazelrc config."""

    def test_config_flag_names_keep_only_the_flag_names_of_the_config(self) -> None:
        names = ci_config_reader.config_flag_names(BAZELRC, "ci")
        self.assertEqual(names, ["--remote_cache", "--jobs", "--test_env"])

    def test_config_flag_names_read_common_build_and_test_lines(self) -> None:
        for command in ("common", "build", "test"):
            with self.subTest(command=command):
                names = ci_config_reader.config_flag_names(f"{command}:ci --jobs=2\n", "ci")
                self.assertEqual(names, ["--jobs"])


if __name__ == "__main__":
    unittest.main()
