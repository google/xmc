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

"""Shared plumbing for the suites that assert on rendered Helm manifests.

Every chart suite needs the same three things: a chart directory that resolves from both bazel
runfiles and the repository root, a rendered template, and a skip that turns into a failure when
CI declared the prerequisites are supposed to be present.
"""

import os
import shutil
import subprocess
import unittest
from typing import NoReturn


def skip_unless_required(reason: str) -> NoReturn:
    """Skips the suite, unless the caller declared the prerequisites are supposed to be present.

    A developer without helm installed should not be blocked, but CI installs it deliberately.
    Skipping there would report a green run for tests that never executed, which is exactly the
    silent gap these suites exist to close, so XMC_REQUIRE_HELM turns the skip into a failure.
    """
    if os.environ.get("XMC_REQUIRE_HELM", "").strip().lower() in ("1", "true", "yes"):
        raise RuntimeError(f"XMC_REQUIRE_HELM is set but {reason}")
    raise unittest.SkipTest(reason)


def require_helm() -> None:
    """Skips or fails the calling suite when the host has no helm binary to render with."""
    if shutil.which("helm") is None:
        skip_unless_required("helm binary not available")


def find_repo_file(relative_path: str) -> str:
    """Resolves a repository-relative path from bazel runfiles or from the repository root.

    A missing file here is a packaging error -- a typo in the path, or a data dependency left
    out of the BUILD target -- not an optional prerequisite like the helm binary. Skipping would
    green the run on every machine that has not set XMC_REQUIRE_HELM, which is the developer
    default, so this raises instead.
    """
    for base in (os.curdir, *(os.path.join(*([os.pardir] * depth)) for depth in range(1, 4))):
        candidate = os.path.join(base, relative_path)
        if os.path.isfile(candidate):
            return candidate
    raise FileNotFoundError(f"{relative_path} not found relative to working directory")


def find_chart_dir() -> str:
    """Locates deploy/helm from the working directory, supporting bazel runfiles and repo root."""
    return os.path.dirname(find_repo_file(os.path.join("deploy", "helm", "Chart.yaml")))


def render_template(show_only: str, values: dict[str, str]) -> str:
    """Renders a single chart template and returns the raw manifest text."""
    return _render(values, "--show-only", show_only)


def render_chart(values: dict[str, str]) -> str:
    """Renders every enabled template and returns the concatenated manifest text.

    Suites asserting on one resource should use render_template. This is for the ones whose
    subject is the set of resources the chart produces rather than any single one of them.
    """
    return _render(values)


def _render(values: dict[str, str], *args: str) -> str:
    """Shells out to helm and returns what it rendered."""
    overrides = [arg for key, value in values.items() for arg in ("--set", f"{key}={value}")]
    command = ["helm", "template", "xmc", find_chart_dir(), *args, *overrides]
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True)
    except subprocess.CalledProcessError as error:
        # CalledProcessError omits captured output, which hides the template error that
        # explains the failure.
        raise RuntimeError(
            f"`{' '.join(command)}` failed:\nstdout: {error.stdout}\nstderr: {error.stderr}"
        ) from error
    return result.stdout
