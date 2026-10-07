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

"""GitHub Actions job summary of the E2E harness outcome."""

from __future__ import annotations

import logging
import os

from test.e2e.lib.outcome import AttemptResult, HarnessResult, Outcome

logger = logging.getLogger("xmc_e2e_runner")

# Set by GitHub Actions to the path of the job summary file of the current step.
_STEP_SUMMARY_ENV = "GITHUB_STEP_SUMMARY"


def render_step_summary(result: HarnessResult) -> str:
    """Renders the outcome, attempt count, failures, and duration as a markdown table.

    Args:
        result: Aggregate result of all harness attempts.

    Returns:
        The markdown summary.
    """
    failures = ", ".join(
        f"{_failure_label(attempt)} (attempt {number})"
        for number, attempt in enumerate(result.attempts, start=1)
        if attempt.outcome is not Outcome.PASSED
    )
    outcome = result.outcome.name.lower().replace("_", " ")
    return (
        "### XMC Headless E2E Summary\n\n"
        "| Outcome | Attempts | Failed phase | Harness duration |\n"
        "| :--- | :--- | :--- | :--- |\n"
        f"| {outcome} | {len(result.attempts)} | {failures or 'none'} | "
        f"{result.duration_seconds:.0f} s |\n"
    )


def append_step_summary(result: HarnessResult) -> None:
    """Appends the summary to the job summary file; does nothing outside GitHub Actions.

    Any error is logged rather than raised, so that reporting never changes the exit code.

    Args:
        result: Aggregate result of all harness attempts.
    """
    path = os.environ.get(_STEP_SUMMARY_ENV)
    if not path:
        return
    try:
        with open(path, "a", encoding="utf-8") as summary_file:
            summary_file.write(render_step_summary(result))
    except Exception as exc:
        # Deliberately broad: the runner calls this outside any try block, and an unexpected
        # rendering or I/O error must not fail a run whose tests passed.
        logger.warning("Could not append the E2E summary to %s: %s", path, exc)


def _failure_label(attempt: AttemptResult) -> str:
    """Names what ended a failed attempt: its failed phase, or else the harness deadline."""
    return attempt.failed_phase.value if attempt.failed_phase else "harness deadline"
