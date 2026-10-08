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

"""Unit tests for lifecycle phases and phase-tagged failures."""

import unittest

from test.e2e.lib.cancellation import HarnessStoppedError
from test.e2e.lib.phases import Phase, PhaseError, phase_scope


class TestPhase(unittest.TestCase):
    """Tests for Phase classification."""

    def test_is_infra_only_for_infrastructure_phases(self):
        expected = {
            Phase.IMAGE_BUILD: False,
            Phase.KIND_CREATE: True,
            Phase.IMAGE_LOAD: True,
            Phase.SPANNER_SETUP: True,
            Phase.HELM_DEPLOY: False,
            Phase.TESTS: False,
        }

        self.assertEqual({phase: phase.is_infra for phase in Phase}, expected)


class TestPhaseScope(unittest.TestCase):
    """Tests for phase_scope failure tagging."""

    def test_wraps_exception_in_phase_error_with_cause(self):
        cause = RuntimeError("gcloud failed")

        with self.assertRaises(PhaseError) as ctx, phase_scope(Phase.SPANNER_SETUP):
            raise cause

        self.assertIs(ctx.exception.phase, Phase.SPANNER_SETUP)
        self.assertIs(ctx.exception.__cause__, cause)
        self.assertIn("spanner-setup", str(ctx.exception))

    def test_keeps_innermost_phase_for_nested_scopes(self):
        with (
            self.assertRaises(PhaseError) as ctx,
            phase_scope(Phase.HELM_DEPLOY),
            phase_scope(Phase.TESTS),
        ):
            raise RuntimeError("playwright failed")

        self.assertIs(ctx.exception.phase, Phase.TESTS)

    def test_does_not_wrap_harness_stop(self):
        with self.assertRaises(HarnessStoppedError), phase_scope(Phase.KIND_CREATE):
            raise HarnessStoppedError()


if __name__ == "__main__":
    unittest.main()
