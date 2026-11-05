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

"""Unit tests for test.e2e.lib.work_unit_state."""

from __future__ import annotations

import unittest
from typing import Any
from unittest import mock

from test.e2e.lib.polling import TransientProbeError
from test.e2e.lib.work_unit_state import (
    UNSPECIFIED_STATE,
    assert_executions_history,
    execution_id_from_name,
    wait_for_work_unit_state,
    work_unit_fetch_error,
    work_unit_name_from_operation,
    work_unit_state,
)

_WORK_UNIT_NAME = "experiments/42/workUnits/7"


def _work_unit(state: str, message: str = "") -> dict[str, Any]:
    """Returns the REST representation of a work unit in the given state."""
    return {
        "name": _WORK_UNIT_NAME,
        "currentWorkUnitExecution": {
            "name": f"{_WORK_UNIT_NAME}/executions/1",
            "status": {"state": state, "systemMessage": {"userFacingText": message}},
        },
    }


class WorkUnitNameTest(unittest.TestCase):
    """Tests for work_unit_name_from_operation."""

    def test_strips_operation_suffix(self) -> None:
        """The work unit name is the operation name without its /operations/ suffix."""
        self.assertEqual(
            work_unit_name_from_operation(f"{_WORK_UNIT_NAME}/operations/6f1c-42"),
            _WORK_UNIT_NAME,
        )

    def test_rejects_malformed_operation_names(self) -> None:
        """Names without a work unit or an operation ID are rejected."""
        for bad_name in (
            "",
            _WORK_UNIT_NAME,
            "experiments/42/operations/6f1c-42",
            f"{_WORK_UNIT_NAME}/operations/",
        ):
            with self.subTest(bad_name=bad_name):
                with self.assertRaises(ValueError):
                    work_unit_name_from_operation(bad_name)


class WorkUnitStateTest(unittest.TestCase):
    """Tests for work_unit_state."""

    def test_reads_current_execution_state(self) -> None:
        """work_unit_state returns the state of the current execution."""
        self.assertEqual(work_unit_state(_work_unit("RUNNING")), "RUNNING")

    def test_defaults_to_unspecified(self) -> None:
        """work_unit_state tolerates missing or null execution fields."""
        work_units: tuple[dict[str, Any], ...] = (
            {},
            {"currentWorkUnitExecution": None},
            {"currentWorkUnitExecution": {"status": {}}},
        )
        for work_unit in work_units:
            with self.subTest(work_unit=work_unit):
                self.assertEqual(work_unit_state(work_unit), UNSPECIFIED_STATE)


class WorkUnitFetchErrorTest(unittest.TestCase):
    """Tests for work_unit_fetch_error."""

    def test_transient_statuses_are_retried_by_polling(self) -> None:
        """Rate limiting and gateway failures map to TransientProbeError."""
        for status in (429, 502, 503, 504):
            with self.subTest(status=status):
                error = work_unit_fetch_error(_WORK_UNIT_NAME, status, "retry later")

                self.assertIsInstance(error, TransientProbeError)
                self.assertEqual(
                    str(error), f"Failed to get {_WORK_UNIT_NAME}: {status} retry later"
                )

    def test_other_statuses_fail_fast(self) -> None:
        """Other failures map to a RuntimeError that polling does not retry."""
        for status in (400, 401, 403, 404, 500):
            with self.subTest(status=status):
                error = work_unit_fetch_error(_WORK_UNIT_NAME, status, "denied")

                self.assertIsInstance(error, RuntimeError)
                self.assertNotIsInstance(error, TransientProbeError)
                self.assertEqual(str(error), f"Failed to get {_WORK_UNIT_NAME}: {status} denied")


@mock.patch("test.e2e.lib.polling.time.sleep")
class WaitForWorkUnitStateTest(unittest.TestCase):
    """Tests for wait_for_work_unit_state."""

    def test_polls_until_expected_state(self, mock_sleep: mock.MagicMock) -> None:
        """Non-terminal states are polled through until the expected state appears."""
        fetch = mock.Mock(
            side_effect=[_work_unit("SCHEDULING"), _work_unit("RUNNING"), _work_unit("COMPLETED")]
        )

        got = wait_for_work_unit_state(fetch, "COMPLETED", poll_interval_seconds=0.5)

        self.assertEqual(work_unit_state(got), "COMPLETED")
        self.assertEqual(fetch.call_count, 3)
        mock_sleep.assert_has_calls([mock.call(0.5), mock.call(0.5)])

    def test_fails_fast_on_other_terminal_state(self, mock_sleep: mock.MagicMock) -> None:
        """A different terminal state raises with the work unit's system message."""
        fetch = mock.Mock(return_value=_work_unit("FAILED", "exit code 50"))

        with self.assertRaisesRegex(RuntimeError, "FAILED instead of COMPLETED: 'exit code 50'"):
            wait_for_work_unit_state(fetch, "COMPLETED")

        fetch.assert_called_once()
        mock_sleep.assert_not_called()

    def test_retries_transient_fetch_failures(self, mock_sleep: mock.MagicMock) -> None:
        """Transient fetch failures are retried until the expected state appears."""
        fetch = mock.Mock(
            side_effect=[TransientProbeError("503 unavailable"), _work_unit("COMPLETED")]
        )

        got = wait_for_work_unit_state(fetch, "COMPLETED", poll_interval_seconds=0.5)

        self.assertEqual(work_unit_state(got), "COMPLETED")
        self.assertEqual(fetch.call_count, 2)
        mock_sleep.assert_called_once_with(0.5)

    def test_propagates_other_fetch_failures(self, mock_sleep: mock.MagicMock) -> None:
        """Fetch failures that are not transient propagate without retrying."""
        fetch = mock.Mock(side_effect=RuntimeError("404 not found"))

        with self.assertRaisesRegex(RuntimeError, "404 not found"):
            wait_for_work_unit_state(fetch, "COMPLETED")

        fetch.assert_called_once()
        mock_sleep.assert_not_called()


class ExecutionIdFromNameTest(unittest.TestCase):
    """Tests for execution_id_from_name."""

    def test_parses_valid_execution_name(self) -> None:
        """Extracts the integer execution ID from a canonical execution resource name."""
        self.assertEqual(
            execution_id_from_name("experiments/42/workUnits/7/executions/2"),
            2,
        )

    def test_rejects_invalid_execution_names(self) -> None:
        """Rejects malformed, missing, or non-numeric execution resource names."""
        bad_names = (
            "",
            "invalid",
            "experiments/42/workUnits/7",
            "experiments/42/workUnits/7/executions/",
            "experiments/42/workUnits/7/executions/abc",
            "other/42/workUnits/7/executions/1",
        )
        for bad_name in bad_names:
            with self.subTest(bad_name=bad_name):
                with self.assertRaises(ValueError):
                    execution_id_from_name(bad_name)


class AssertExecutionsHistoryTest(unittest.TestCase):
    """Tests for assert_executions_history."""

    def test_valid_executions_history_passes(self) -> None:
        """Valid descending active/inactive execution sequence passes verification."""
        valid_executions = [
            {"name": "experiments/42/workUnits/7/executions/2", "state": "ACTIVE"},
            {"name": "experiments/42/workUnits/7/executions/1", "state": "INACTIVE"},
        ]
        assert_executions_history(valid_executions, expected_min_count=2)

    def test_numeric_enum_state_passes(self) -> None:
        """Integer protobuf enum values for ACTIVE (1) and INACTIVE (2) pass verification."""
        valid_executions = [
            {"name": "experiments/42/workUnits/7/executions/2", "state": 1},
            {"name": "experiments/42/workUnits/7/executions/1", "state": 2},
        ]
        assert_executions_history(valid_executions, expected_min_count=2)

    def test_rejects_insufficient_executions(self) -> None:
        """Fewer executions than expected_min_count raises ValueError."""
        with self.assertRaises(ValueError):
            assert_executions_history([], expected_min_count=2)
        with self.assertRaises(ValueError):
            assert_executions_history(
                [{"name": "experiments/42/workUnits/7/executions/1", "state": "ACTIVE"}],
                expected_min_count=2,
            )

    def test_rejects_missing_or_empty_execution_name(self) -> None:
        """Executions without a name or with an empty name raise ValueError."""
        for bad_execution in ({"state": "INACTIVE"}, {"name": "", "state": "INACTIVE"}):
            with self.subTest(bad_execution=bad_execution):
                with self.assertRaisesRegex(ValueError, "Execution is missing a name"):
                    assert_executions_history(
                        [
                            {"name": "experiments/42/workUnits/7/executions/2", "state": "ACTIVE"},
                            bad_execution,
                        ],
                        expected_min_count=2,
                    )

    def test_rejects_non_descending_executions(self) -> None:
        """Ascending or equal execution IDs raise RuntimeError."""
        ascending = [
            {"name": "experiments/42/workUnits/7/executions/1", "state": "ACTIVE"},
            {"name": "experiments/42/workUnits/7/executions/2", "state": "INACTIVE"},
        ]
        with self.assertRaisesRegex(RuntimeError, "Executions not in descending order"):
            assert_executions_history(ascending, expected_min_count=2)

    def test_rejects_inactive_primary_execution(self) -> None:
        """Inactive latest execution raises RuntimeError."""
        bad_primary = [
            {"name": "experiments/42/workUnits/7/executions/2", "state": "INACTIVE"},
            {"name": "experiments/42/workUnits/7/executions/1", "state": "INACTIVE"},
        ]
        with self.assertRaisesRegex(RuntimeError, "Expected primary execution 2 to be ACTIVE"):
            assert_executions_history(bad_primary, expected_min_count=2)

    def test_rejects_active_historical_execution(self) -> None:
        """Active historical execution raises RuntimeError."""
        bad_historical = [
            {"name": "experiments/42/workUnits/7/executions/2", "state": "ACTIVE"},
            {"name": "experiments/42/workUnits/7/executions/1", "state": "ACTIVE"},
        ]
        with self.assertRaisesRegex(
            RuntimeError, "Expected historical execution 1 .* to be INACTIVE"
        ):
            assert_executions_history(bad_historical, expected_min_count=2)


if __name__ == "__main__":
    unittest.main()
