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

"""Reads the CI configuration that tools/run_affected_tests.py mirrors.

PyYAML is not available to Bazel py_test targets, so the GitHub Actions files are read
with regular expressions that rely on their flat layout: in an action.yml, inputs are
indented by two spaces, steps start with `    - ` under `  steps:`, step keys are
indented by six spaces, and their block lines, such as env entries and `run: |` script
lines, by eight or more. Workflow steps are indented two spaces deeper. Scalars may be
plain, single-quoted, or double-quoted, and comment and blank lines are skipped.
"""

import json
import re
import shlex
from collections.abc import Mapping

# A plain, single-quoted, or double-quoted YAML scalar, optionally followed by a comment.
SCALAR_PATTERN = re.compile(r"""(?:'((?:[^']|'')*)'|("(?:[^"\\]|\\.)*")|(.*?))(?:\s+#.*)?""")
# An input of an action.yml and the lines that define it.
INPUT_PATTERN = re.compile(r"^  ([\w-]+):\n((?:(?:    .*)?\n)*)", re.MULTILINE)
# The .bazelrc commands whose config lines `bazel test --config=NAME` applies.
TEST_CONFIG_COMMANDS = ("common", "build", "test")


def yaml_scalar(text: str) -> str:
    """Returns the value of a plain, single-quoted, or double-quoted YAML scalar."""
    match = SCALAR_PATTERN.fullmatch(text.strip())
    if match is None:
        raise ValueError(f"{text!r} is not a single-line YAML scalar.")
    single, double, plain = match.groups()
    if single is not None:
        return single.replace("''", "'")
    return str(json.loads(double)) if double is not None else plain


def step_key_lines(step: str, key: str) -> list[str]:
    """Returns the unquoted inline value, or the block lines, of a step key; [] if unset.

    Block lines follow `key:` alone or with a literal block indicator such as `|` or `|-`;
    they are stripped, and comment and blank lines are skipped.
    """
    pattern = rf"^      {re.escape(key)}:(.*)\n((?:        .*\n|[ \t]*(?:#.*)?\n)*)"
    match = re.search(pattern, step, re.MULTILINE)
    if not match:
        return []
    if not re.fullmatch(r"(?:\|[-+0-9]*)?\s*(?:#.*)?", match[1].strip()):
        return [yaml_scalar(match[1])]
    lines = (line.strip() for line in match[2].splitlines())
    return [line for line in lines if line and not line.startswith("#")]


def action_steps(action_text: str) -> dict[str, str]:
    """Maps the step names of an action.yml to their text, re-indented like other keys."""
    steps_text = action_text.partition("\n  steps:\n")[2]
    steps = ["      " + step for step in re.split(r"^    - ", steps_text, flags=re.MULTILINE)[1:]]
    return {"".join(step_key_lines(step, "name")): step for step in steps}


def action_input_defaults(action_text: str) -> dict[str, str]:
    """Maps the inputs of an action.yml to their defaults.

    Raises ValueError naming the input if a default is missing or unquoted, since an
    unquoted scalar such as 1.10 may not read as the string that CI passes.
    """
    inputs_text = action_text.partition("\ninputs:\n")[2].partition("\nruns:\n")[0] + "\n"
    defaults: dict[str, str] = {}
    for name, definition in INPUT_PATTERN.findall(inputs_text):
        default = re.search(r"^    default:(.*)$", definition, re.MULTILINE)
        if not default or not default[1].strip().startswith(("'", '"')):
            raise ValueError(f"Input {name!r} needs a quoted default.")
        defaults[name] = yaml_scalar(default[1])
    return defaults


def substitute_inputs(text: str, inputs: Mapping[str, str]) -> str:
    """Replaces the ${{ inputs.NAME }} expressions in text with their values in inputs.

    Raises ValueError naming an input that inputs does not define.
    """

    def input_value(match: re.Match[str]) -> str:
        if match[1] not in inputs:
            raise ValueError(f"Input {match[1]!r} is not defined.")
        return inputs[match[1]]

    return re.sub(r"\$\{\{\s*inputs\.([\w-]+)\s*\}\}", input_value, text)


def step_mapping(step: str, key: str) -> dict[str, str]:
    """Returns the entries of a mapping key of a step, such as env or with, unquoted."""
    entries = (line.partition(":") for line in step_key_lines(step, key))
    return {yaml_scalar(name): yaml_scalar(value) for name, _, value in entries}


def step_env(step: str, inputs: Mapping[str, str]) -> dict[str, str]:
    """Returns the env of a step, with ${{ inputs.NAME }} expressions substituted."""
    env = step_mapping(step, "env")
    return {name: substitute_inputs(value, inputs) for name, value in env.items()}


def step_working_directory(step: str, inputs: Mapping[str, str]) -> str:
    """Returns the working directory of a step with inputs substituted, "." if unset."""
    return substitute_inputs("".join(step_key_lines(step, "working-directory")), inputs) or "."


def step_condition(step: str) -> str:
    """Returns the if: condition of a step without a ${{ }} wrapper, "" if unset."""
    condition = "".join(step_key_lines(step, "if"))
    expression = re.fullmatch(r"\$\{\{\s*(.*?)\s*\}\}", condition)
    return expression[1] if expression else condition


def step_commands(step: str) -> list[str]:
    """Returns the commands of a step's run script, joining shell line continuations."""
    script = "\n".join(step_key_lines(step, "run"))
    return re.sub(r"\s*\\\n", " ", script).splitlines()


def expand_env_references(command: str, env: Mapping[str, str]) -> str:
    """Replaces the ${NAME} references in command that env defines with their values."""
    return re.sub(r"\$\{(\w+)\}", lambda match: env.get(match[1], match[0]), command)


def paths_filter_patterns(action_text: str, filter_name: str) -> list[str]:
    """Returns the patterns of a paths-filter filter, unquoted, skipping comments and blanks.

    The patterns are the `- ` items up to the next line, other than an item, that is not
    indented deeper than the filter.
    """
    pattern = rf"^( *){re.escape(filter_name)}:\n((?:\1(?: |- ).*\n|[ \t]*(?:#.*)?\n)*)"
    match = re.search(pattern, action_text, re.MULTILINE)
    items = (line.strip() for line in (match[2] if match else "").splitlines())
    return [yaml_scalar(item[2:]) for item in items if item.startswith("- ")]


def workflow_step_inputs(workflow_text: str, action_path: str) -> list[dict[str, str]]:
    """Returns the with: inputs of each workflow step that uses action_path, in order.

    Workflow steps are re-indented like action steps before they are read.
    """
    chunks = re.split(r"^      - ", workflow_text, flags=re.MULTILINE)[1:]
    steps = ("      " + re.sub(r"^  ", "", chunk, flags=re.MULTILINE) for chunk in chunks)
    uses_action = [step for step in steps if step_key_lines(step, "uses") == [action_path]]
    return [step_mapping(step, "with") for step in uses_action]


def container_images(workflow_text: str) -> list[str]:
    """Returns the container images that a workflow file names, unquoted and in order."""
    return re.findall(r"^\s+image:\s*['\"]?([^'\"\s]+)", workflow_text, re.MULTILINE)


def config_flag_names(bazelrc_text: str, config: str) -> list[str]:
    """Returns the names of the flags that `bazel test --config=config` takes from .bazelrc.

    These are the flags of the config's common:, build:, and test: lines, in order; for
    example, `build:ci --jobs=14` gives "--jobs" for config "ci". Flag values are dropped.
    """
    commands = {f"{command}:{config}" for command in TEST_CONFIG_COMMANDS}
    words = (
        word
        for line in bazelrc_text.splitlines()
        if (line.split() or [""])[0] in commands
        for word in shlex.split(line, comments=True)[1:]
    )
    return [word.partition("=")[0] for word in words if word.startswith("-")]
