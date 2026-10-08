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

"""Harness outcomes, their process exit codes, and per-attempt results."""

from __future__ import annotations

import dataclasses
import enum

from test.e2e.lib.phases import Phase


class Outcome(enum.IntEnum):
    """Final harness outcome; each value is the process exit code of ``runner.py``."""

    PASSED = 0
    PRODUCT_FAILURE = 1
    INFRA_FAILURE = 3
    HARNESS_ERROR = 4


@dataclasses.dataclass(frozen=True)
class AttemptResult:
    """Result of one full harness attempt (setup, tests, and teardown).

    Attributes:
        outcome: Classified outcome of the attempt.
        failed_phase: Lifecycle phase that failed, or None if the attempt passed.
        images_built: Whether the attempt finished building the container images, which a
            retry then reuses.
    """

    outcome: Outcome
    failed_phase: Phase | None = None
    images_built: bool = False

    @classmethod
    def for_failed_phase(cls, phase: Phase, images_built: bool) -> AttemptResult:
        """Classifies a phase failure as an infra failure or a product failure.

        Args:
            phase: The lifecycle phase that failed.
            images_built: Whether the attempt finished building the container images.

        Returns:
            The classified attempt result.
        """
        outcome = Outcome.INFRA_FAILURE if phase.is_infra else Outcome.PRODUCT_FAILURE
        return cls(outcome=outcome, failed_phase=phase, images_built=images_built)


@dataclasses.dataclass(frozen=True)
class HarnessResult:
    """Aggregate result of every harness attempt.

    Attributes:
        attempts: Attempt results in execution order; never empty.
    """

    attempts: tuple[AttemptResult, ...]

    def __post_init__(self) -> None:
        if not self.attempts:
            raise ValueError("HarnessResult requires at least one attempt")

    @property
    def outcome(self) -> Outcome:
        """The final outcome, which is the outcome of the last attempt."""
        return self.attempts[-1].outcome
