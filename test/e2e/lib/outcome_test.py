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

"""Unit tests for harness outcomes and attempt results."""

import unittest

from test.e2e.lib.outcome import AttemptResult, HarnessResult, Outcome
from test.e2e.lib.phases import Phase


class TestOutcome(unittest.TestCase):
    """Tests for the exit code contract."""

    def test_exit_codes_match_documented_contract(self):
        expected = {"PASSED": 0, "PRODUCT_FAILURE": 1, "INFRA_FAILURE": 3, "HARNESS_ERROR": 4}

        self.assertEqual({outcome.name: int(outcome) for outcome in Outcome}, expected)


class TestAttemptResult(unittest.TestCase):
    """Tests for failure classification."""

    def test_for_failed_phase_classifies_by_phase(self):
        expected_exit_codes = {
            Phase.IMAGE_BUILD: 1,
            Phase.KIND_CREATE: 3,
            Phase.IMAGE_LOAD: 3,
            Phase.SPANNER_SETUP: 3,
            Phase.HELM_DEPLOY: 1,
            Phase.TESTS: 1,
        }

        exit_codes = {
            phase: int(AttemptResult.for_failed_phase(phase, images_built=True).outcome)
            for phase in Phase
        }

        self.assertEqual(exit_codes, expected_exit_codes)

    def test_for_failed_phase_records_phase_and_built_images(self):
        result = AttemptResult.for_failed_phase(Phase.SPANNER_SETUP, images_built=True)

        self.assertEqual((result.failed_phase, result.images_built), (Phase.SPANNER_SETUP, True))


class TestHarnessResult(unittest.TestCase):
    """Tests for the aggregate harness result."""

    def test_outcome_is_last_attempt_outcome(self):
        result = HarnessResult(
            attempts=(
                AttemptResult.for_failed_phase(Phase.SPANNER_SETUP, images_built=True),
                AttemptResult(outcome=Outcome.PASSED),
            )
        )

        self.assertIs(result.outcome, Outcome.PASSED)

    def test_rejects_empty_attempts(self):
        with self.assertRaises(ValueError):
            HarnessResult(attempts=())


if __name__ == "__main__":
    unittest.main()
