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

"""Unit tests for bounded retries of transient Spanner command failures."""

import subprocess
import unittest
from unittest.mock import MagicMock, call, patch

from test.e2e.lib import cancellation, spanner_retry
from test.e2e.lib.cancellation import HarnessStoppedError
from test.e2e.lib.spanner_retry import (
    ErrorClass,
    RetryBudget,
    classify_error,
    is_already_exists_error,
    redact_error_output,
    run_with_transient_retry,
)

_BUDGET = RetryBudget(
    max_attempts=4, base_delay_seconds=15, attempt_timeout_seconds=120, total_seconds=240
)


def _gcloud_error(
    stderr: str | bytes, cmd: list[str] | None = None
) -> subprocess.CalledProcessError:
    return subprocess.CalledProcessError(
        returncode=1, cmd=cmd or ["gcloud", "spanner", "databases", "create"], stderr=stderr
    )


_TRANSIENT = _gcloud_error("ERROR: (gcloud.spanner.databases.create) Deadline exceeded")
_INTERNAL = _gcloud_error("ERROR: (gcloud.spanner.databases.create) INTERNAL: bad proto bundle")
_PERMANENT = _gcloud_error("ERROR: INVALID_ARGUMENT: Error parsing Spanner DDL statement")
_TOKEN_REFRESH_ERROR = (
    "ERROR: There was a problem refreshing your current auth tokens: ('Unable to acquire "
    'impersonated credentials\', \'{"error": {"code": 503, "message": "The service is '
    'currently unavailable.", "status": "UNAVAILABLE"}}\')'
)


class TestRetryBudget(unittest.TestCase):
    """Tests for the backoff of a retry budget."""

    def test_delay_doubles_without_jitter(self):
        budget = RetryBudget(
            max_attempts=3, base_delay_seconds=5, attempt_timeout_seconds=45, total_seconds=90
        )

        self.assertEqual([budget.delay_seconds(attempt) for attempt in (1, 2)], [5, 10])

    def test_delay_jitter_stays_within_fraction(self):
        budget = RetryBudget(4, 15, 120, 240, jitter_fraction=0.2)

        with patch.object(spanner_retry.random, "uniform", side_effect=lambda low, high: high):
            longest = [budget.delay_seconds(attempt) for attempt in (1, 2, 3)]
        with patch.object(spanner_retry.random, "uniform", side_effect=lambda low, high: low):
            shortest = [budget.delay_seconds(attempt) for attempt in (1, 2, 3)]

        self.assertEqual(longest, [18, 36, 72])
        self.assertEqual(shortest, [12, 24, 48])


class TestClassifyError(unittest.TestCase):
    """Tests for retry classification of failed attempts."""

    def test_classifies_errors(self):
        transient, internal, permanent = (
            ErrorClass.TRANSIENT,
            ErrorClass.INTERNAL,
            ErrorClass.PERMANENT,
        )
        cases = [
            ("failed operation deadline", _TRANSIENT, transient),
            ("unavailable status", _gcloud_error("UNAVAILABLE: Service unavailable."), transient),
            ("grpc unavailable bytes", _gcloud_error(b"code = Unavailable desc = eof"), transient),
            ("grpc deadline", _gcloud_error("code = DeadlineExceeded desc = x"), transient),
            ("aborted status", _gcloud_error("ABORTED: Transaction was aborted."), transient),
            ("quota status", _gcloud_error("status: RESOURCE_EXHAUSTED, db limit"), transient),
            ("http status field", _gcloud_error("response: <{'status': '503'}>"), transient),
            ("json code field", _gcloud_error('{"error": {"code": 429}}'), transient),
            ("http status line", _gcloud_error("HTTP/1.1 504 Gateway Timeout"), transient),
            ("token refresh", _gcloud_error(_TOKEN_REFRESH_ERROR), transient),
            ("quota message", _gcloud_error("Operation failed: Quota exceeded for DDL"), transient),
            ("concurrent ddl", _gcloud_error("Too many concurrent schema changes"), transient),
            ("pending ddl", _gcloud_error("There are 3 pending DDL operations"), transient),
            ("timeout", subprocess.TimeoutExpired(["gcloud"], 120), transient),
            ("internal status", _INTERNAL, internal),
            ("failed operation internal", _gcloud_error("Internal error encountered."), internal),
            ("grpc internal", _gcloud_error(b"rpc error: code = Internal desc = x"), internal),
            ("ddl syntax", _PERMANENT, permanent),
            ("ddl echo string", _gcloud_error("Foo (Name STRING(500) NOT NUL)"), permanent),
            ("ddl echo column", _gcloud_error("Syntax error on line 1, column 502"), permanent),
            ("ddl echo code", _gcloud_error("Code STRING(503), column 504"), permanent),
            (
                "markers only in command line",
                _gcloud_error("INVALID_ARGUMENT: bad ddl", ["gcloud", "/tmp/429/unavailable"]),
                permanent,
            ),
            ("permission denied", _gcloud_error("PERMISSION_DENIED: project 1234"), permanent),
            ("unavailable in identifier", _gcloud_error("Invalid maxUnavailable: -1"), permanent),
            ("already exists", _gcloud_error("ALREADY_EXISTS: Database exists"), permanent),
            ("http 500", _gcloud_error("HTTP 500 Internal Server Error"), permanent),
            ("http 502", _gcloud_error("HTTP 502 Bad Gateway"), permanent),
            (
                "transport errors",
                _gcloud_error("Connection reset by peer; Connection aborted; Read timed out"),
                permanent,
            ),
            ("missing binary", FileNotFoundError("No such file: 'gcloud' unavailable"), permanent),
        ]
        for name, exc, expected in cases:
            with self.subTest(name):
                self.assertIs(classify_error(exc), expected)

    def test_detects_already_exists_in_captured_output_only(self):
        self.assertTrue(is_already_exists_error(_gcloud_error("ALREADY_EXISTS: Database d")))
        self.assertFalse(is_already_exists_error(_gcloud_error("NOT_FOUND", ["already_exists"])))
        self.assertFalse(is_already_exists_error(FileNotFoundError("already exists")))


class TestRedactErrorOutput(unittest.TestCase):
    """Tests for masking project identifiers in logged error output."""

    def test_masks_project_ids_and_numbers(self):
        text = (
            "Database already exists: projects/my-proj-123/instances/inst/databases/db\n"
            "API has not been used in project 123456789012 before\n"
            '{"project_number": "987654321", "project": 42}'
        )

        self.assertEqual(
            redact_error_output(text),
            "Database already exists: projects/***/instances/inst/databases/db\n"
            "API has not been used in project *** before\n"
            '{"project_number": "***", "project": ***}',
        )

    def test_masks_project_numbers_in_every_key_spelling(self):
        cases = [
            ('{"projectNumber": "123456789012"}', '{"projectNumber": "***"}'),
            ("projectNumber: '123456789012'", "projectNumber: '***'"),
            ("project_number=987654321", "project_number=***"),
            ("project-number: 55", "project-number: ***"),
            ("consumer: projects/123456789012", "consumer: projects/***"),
            ("containerInfo: '123456789012'", "containerInfo: '***'"),
            ("overview?project=123456789012 then retry", "overview?project=*** then retry"),
        ]
        for text, expected in cases:
            with self.subTest(text):
                self.assertEqual(redact_error_output(text), expected)


class TestRunWithTransientRetry(unittest.TestCase):
    """Tests for the bounded retry loop, on a fake clock that only advances when waited on."""

    def setUp(self):
        self.now = 0.0
        self.sleep = MagicMock(side_effect=self._advance)
        for target, name, fake in (
            (spanner_retry.time, "monotonic", lambda: self.now),
            (cancellation.CONTROLLER, "wait", self.sleep),
        ):
            patcher = patch.object(target, name, fake)
            patcher.start()
            self.addCleanup(patcher.stop)

    def _advance(self, seconds: float) -> None:
        self.now += seconds

    def test_stops_retrying_when_harness_halts_during_backoff(self):
        self.sleep.side_effect = HarnessStoppedError()
        action = MagicMock(side_effect=_TRANSIENT)

        with self.assertRaises(HarnessStoppedError):
            run_with_transient_retry(action, "Creating db", _BUDGET)

        action.assert_called_once()

    def test_retries_transient_failure_until_success(self):
        action = MagicMock(side_effect=[_TRANSIENT, None])

        run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertEqual(action.call_args_list, [call(1, 120), call(2, 120)])
        self.sleep.assert_called_once_with(15)

    def test_raises_permanent_failure_immediately(self):
        action = MagicMock(side_effect=_PERMANENT)

        with self.assertRaises(subprocess.CalledProcessError) as ctx:
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertIs(ctx.exception, _PERMANENT)
        action.assert_called_once()
        self.sleep.assert_not_called()

    def test_retries_internal_failure_only_once(self):
        action = MagicMock(side_effect=[_INTERNAL, _TRANSIENT, _INTERNAL])

        with self.assertRaises(subprocess.CalledProcessError) as ctx:
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertIs(ctx.exception, _INTERNAL)
        self.assertEqual(self.sleep.call_args_list, [call(15), call(30)])

    def test_gives_up_after_max_attempts(self):
        action = MagicMock(side_effect=_TRANSIENT)

        with self.assertRaises(subprocess.CalledProcessError):
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertEqual(action.call_count, 4)
        self.assertEqual(self.sleep.call_args_list, [call(15), call(30), call(60)])

    def test_bounds_attempts_and_waits_by_total_budget(self):
        def _time_out(attempt: int, timeout: float) -> None:
            self._advance(timeout)
            raise subprocess.TimeoutExpired(["gcloud"], timeout)

        action = MagicMock(side_effect=_time_out)

        with self.assertRaises(subprocess.TimeoutExpired):
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertEqual(action.call_args_list, [call(1, 120), call(2, 105)])
        self.sleep.assert_called_once_with(15)
        self.assertEqual(self.now, 240)

    def _fail_first_attempt_after(self, seconds: float) -> MagicMock:
        def _action(attempt: int, _timeout: float) -> None:
            if attempt == 1:
                self._advance(seconds)
                raise _TRANSIENT

        return MagicMock(side_effect=_action)

    def test_skips_retry_that_would_get_less_than_30_seconds_of_budget(self):
        budget = RetryBudget(
            max_attempts=2, base_delay_seconds=15, attempt_timeout_seconds=240, total_seconds=240
        )
        # The first attempt fails at 195 s or at 196 s, so 30 s or 29 s remain after the wait.
        retried = self._fail_first_attempt_after(195)
        run_with_transient_retry(retried, "Creating db", budget)
        self.now = 0.0
        not_retried = self._fail_first_attempt_after(196)
        with self.assertRaises(subprocess.CalledProcessError):
            run_with_transient_retry(not_retried, "Creating db", budget)

        self.assertEqual(retried.call_args_list, [call(1, 240), call(2, 30)])
        not_retried.assert_called_once_with(1, 240)

    def test_logs_redacted_error_output_tail_for_every_failed_attempt(self):
        stderr = "HEAD" + "x" * 3000 + " projects/my-proj/instances/i: Deadline exceeded TAIL"
        action = MagicMock(side_effect=[_gcloud_error(stderr), _PERMANENT])

        with (
            self.assertLogs("xmc_e2e_runner", level="WARNING") as logs,
            self.assertRaises(subprocess.CalledProcessError),
        ):
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertEqual(len(logs.output), 2)
        self.assertIn(
            "Creating db failed with CalledProcessError (attempt 1/4, retrying in 15s)",
            logs.output[0],
        )
        self.assertIn("projects/***/instances/i: Deadline exceeded TAIL", logs.output[0])
        self.assertNotIn("HEAD", logs.output[0])
        self.assertIn("(attempt 2/4, not retrying)", logs.output[1])
        self.assertIn("Error parsing Spanner DDL statement", logs.output[1])

    def test_redacts_error_output_before_truncating_it(self):
        # Truncating the 2070 characters first would cut "projects/my-secret-pro" off and log
        # the rest of the project ID unmasked.
        stderr = "projects/my-secret-project-id/" + "x" * 2040
        action = MagicMock(side_effect=_gcloud_error(stderr))

        with (
            self.assertLogs("xmc_e2e_runner", level="WARNING") as logs,
            self.assertRaises(subprocess.CalledProcessError),
        ):
            run_with_transient_retry(action, "Creating db", _BUDGET)

        self.assertIn("cts/***/xxx", logs.output[0])
        self.assertNotIn("ject-id", logs.output[0])


if __name__ == "__main__":
    unittest.main()
