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
        for work_unit in (
            {},
            {"currentWorkUnitExecution": None},
            {"currentWorkUnitExecution": {"status": {}}},
        ):
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


if __name__ == "__main__":
    unittest.main()
