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

"""Run-level retry policy: re-run the full harness once after an early infra failure."""

from __future__ import annotations

import dataclasses
import logging
import os
import time
from collections.abc import Callable

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.orchestrator import E2EOrchestrator
from test.e2e.lib.outcome import AttemptResult, HarnessResult, Outcome
from test.e2e.lib.phases import PhaseError

logger = logging.getLogger("xmc_e2e_runner")

# Total harness attempts: the first run plus a single retry after an infra failure.
MAX_ATTEMPTS = 2
# A retry starts only if the failed attempt, including its teardown, ended within this many
# seconds of the harness start, so that a slow failure is not repeated.
RETRY_START_LIMIT_SECONDS = 480


def run_attempt(cfg: E2EConfig) -> AttemptResult:
    """Runs one full harness attempt and classifies its result.

    Teardown has completed when this returns. Exceptions outside the lifecycle phases
    propagate unclassified.

    Args:
        cfg: Configuration for this attempt.

    Returns:
        The classified result of the attempt.
    """
    orchestrator = E2EOrchestrator(cfg)
    try:
        orchestrator.run()
    except PhaseError as error:
        logger.exception("E2E phase %s failed", error.phase.value)
        return AttemptResult.for_failed_phase(error.phase, orchestrator.images_built)
    return AttemptResult(Outcome.PASSED, images_built=orchestrator.images_built)


def should_retry(
    cfg: E2EConfig, result: AttemptResult, attempt: int, elapsed_seconds: float
) -> bool:
    """Decides whether to re-run the harness after a finished attempt.

    Args:
        cfg: Configuration of the run. ``--keep-cluster`` skips the teardown that a retry
            depends on, so it disables retries.
        result: Result of the attempt that just finished.
        attempt: 1-based number of that attempt.
        elapsed_seconds: Seconds since the harness started, including that attempt's
            teardown.

    Returns:
        True only for an infra failure with attempts remaining that ended within the retry
        window.
    """
    return (
        result.outcome is Outcome.INFRA_FAILURE
        and attempt < MAX_ATTEMPTS
        and elapsed_seconds <= RETRY_START_LIMIT_SECONDS
        and not cfg.keep_cluster
    )


def attempt_config(cfg: E2EConfig, attempt: int, reuse_images: bool) -> E2EConfig:
    """Returns the configuration of an attempt.

    Every attempt writes screenshots and diagnostics to its own ``attempt-<n>`` directory. A
    retry gets fresh Spanner database names and a fresh temporary directory, and skips the
    image build when the failed attempt had finished it.

    Args:
        cfg: Configuration of the run.
        attempt: 1-based number of the attempt.
        reuse_images: Whether the previous attempt finished building the container images.

    Returns:
        The configuration of the attempt.
    """
    screenshots_dir = os.path.join(cfg.screenshots_dir, f"attempt-{attempt}")
    if attempt == 1:
        return dataclasses.replace(cfg, screenshots_dir=screenshots_dir)
    return dataclasses.replace(
        cfg,
        screenshots_dir=screenshots_dir,
        skip_build=cfg.skip_build or reuse_images,
        ess_db="",
        xid_db="",
        temp_dir="",
    )


def run_with_infra_retry(
    cfg: E2EConfig, attempt_runner: Callable[[E2EConfig], AttemptResult]
) -> HarnessResult:
    """Runs the harness, retrying once after an early infra failure.

    Args:
        cfg: Configuration of the run.
        attempt_runner: Runs one full attempt (setup, tests, teardown) and classifies it.

    Returns:
        The results of all attempts.
    """
    start = time.monotonic()
    attempts = [attempt_runner(attempt_config(cfg, 1, reuse_images=False))]
    while should_retry(cfg, attempts[-1], len(attempts), time.monotonic() - start):
        failed = attempts[-1]
        _announce_retry(failed, len(attempts))
        retry_cfg = attempt_config(cfg, len(attempts) + 1, failed.images_built)
        attempts.append(attempt_runner(retry_cfg))
    return HarnessResult(attempts=tuple(attempts))


def _announce_retry(failed: AttemptResult, attempt: int) -> None:
    """Prints a GitHub Actions warning annotation, which stays readable in local runs."""
    phase = failed.failed_phase.value if failed.failed_phase else "unknown"
    print(
        f"::warning::E2E infra failure in phase {phase} on attempt {attempt}/{MAX_ATTEMPTS}; "
        "retrying",
        flush=True,
    )
