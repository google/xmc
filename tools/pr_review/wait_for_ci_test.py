#!/usr/bin/env python3
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

"""Unit tests for wait_for_ci tool."""

import argparse
import json
import subprocess
import unittest
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review import wait_for_ci
from tools.pr_review.wait_for_ci import (
    FAILED,
    PASSED,
    PENDING,
    CheckResult,
    MalformedResponseError,
    build_parser,
    classify_check,
    parse_rollup,
    parse_run_reference,
    trim_job_log,
)

_RUN_URL = "https://github.com/google/xmc/actions/runs/111/job/999"


def _check_run(name, status="COMPLETED", conclusion="SUCCESS", url=_RUN_URL):
    """Builds a CheckRun rollup entry."""
    return {
        "__typename": "CheckRun",
        "name": name,
        "status": status,
        "conclusion": conclusion,
        "detailsUrl": url,
    }


def _completed(stdout="", returncode=0, stderr=""):
    """Builds a fake subprocess.CompletedProcess."""
    return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)


def _rollup(*entries):
    """Builds a fake `gh pr view --json statusCheckRollup` result."""
    return _completed(json.dumps({"statusCheckRollup": list(entries)}))


def _details(ctx) -> dict[str, Any]:
    """Returns the non-null details mapping of a captured HandledError."""
    details = ctx.exception.details
    assert details is not None
    return dict(details)


class FakeClock:
    """Monotonic clock advanced by the fake sleep function."""

    def __init__(self):
        self.now = 0.0
        self.sleeps = []

    def time(self):
        return self.now

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds


class TestClassifyCheck(unittest.TestCase):
    """Tests for classify_check normalization."""

    def test_check_run_states(self):
        """CheckRun status/conclusion pairs map to the expected state."""
        cases = [
            ("IN_PROGRESS", "", PENDING),
            ("QUEUED", "", PENDING),
            ("COMPLETED", "SUCCESS", PASSED),
            ("COMPLETED", "SKIPPED", PASSED),
            ("COMPLETED", "NEUTRAL", PASSED),
            ("COMPLETED", "FAILURE", FAILED),
            ("COMPLETED", "CANCELLED", FAILED),
            ("COMPLETED", "TIMED_OUT", FAILED),
        ]
        for status, conclusion, want in cases:
            with self.subTest(status=status, conclusion=conclusion):
                got = classify_check(_check_run("c", status=status, conclusion=conclusion))
                self.assertEqual(got.state, want)

    def test_status_context_states(self):
        """Legacy StatusContext states map to the expected state."""
        cases = [
            ("PENDING", PENDING),
            ("EXPECTED", PENDING),
            ("SUCCESS", PASSED),
            ("ERROR", FAILED),
        ]
        for raw, want in cases:
            with self.subTest(state=raw):
                entry = {
                    "__typename": "StatusContext",
                    "context": "cla/google",
                    "state": raw,
                    "targetUrl": "https://cla.example",
                }
                got = classify_check(entry)
                self.assertEqual(got, CheckResult("cla/google", want, raw, "https://cla.example"))

    def test_check_run_null_fields(self):
        """Null conclusion/detailsUrl on pending runs are tolerated."""
        entry = _check_run("x", status="IN_PROGRESS", conclusion=None, url=None)
        self.assertEqual(classify_check(entry), CheckResult("x", PENDING, "", ""))

    def test_null_names_become_empty_strings(self):
        """Explicit JSON null names/contexts normalize to empty strings."""
        run = _check_run(None)
        context = {"__typename": "StatusContext", "context": None, "state": "SUCCESS"}
        self.assertEqual(classify_check(run).name, "")
        self.assertEqual(classify_check(context).name, "")


class TestHelpers(unittest.TestCase):
    """Tests for small pure helpers."""

    def test_parse_run_reference(self):
        """Repo, run and job IDs are parsed from Actions URLs only."""
        self.assertEqual(parse_run_reference(_RUN_URL), ("google/xmc", "111", "999"))
        self.assertEqual(
            parse_run_reference("https://github.com/o/r/actions/runs/5"), ("o/r", "5", None)
        )
        self.assertIsNone(parse_run_reference("https://cla.developers.google.com/about"))
        self.assertIsNone(parse_run_reference(""))

    def test_trim_job_log_ends_at_last_error(self):
        """Trimming drops post-error cleanup and bounds the line count."""
        log = "a\nb\n##[error]lint failed\n##[error]exit 1\nPost job cleanup.\nmore"
        self.assertEqual(trim_job_log(log, 2), "##[error]lint failed\n##[error]exit 1")
        self.assertEqual(trim_job_log(log, 100).splitlines()[0], "a")

    def test_trim_job_log_without_error_marker(self):
        """Without an error marker, the plain tail is returned."""
        self.assertEqual(trim_job_log("a\nb\nc\nd", 2), "c\nd")
        self.assertEqual(trim_job_log("", 5), "")

    def test_build_parser_defaults(self):
        """Parser defaults match documented constants."""
        args = build_parser().parse_args(["148"])
        self.assertEqual(args.pr, "148")
        self.assertIsNone(args.repo)
        self.assertEqual(args.timeout, wait_for_ci.DEFAULT_TIMEOUT_SECONDS)
        self.assertEqual(args.interval, wait_for_ci.DEFAULT_INTERVAL_SECONDS)
        self.assertEqual(args.log_lines, wait_for_ci.DEFAULT_LOG_LINES)

    def test_build_parser_rejects_non_positive_values(self):
        """Numeric flags reject zero, negative, and non-integer values."""
        for flag in ("--timeout", "--interval", "--log-lines"):
            for value in ("0", "-5", "abc"):
                with self.subTest(flag=flag, value=value):
                    with patch("sys.stderr"), self.assertRaises(SystemExit):
                        build_parser().parse_args(["148", flag, value])

    def test_parse_rollup_rejects_bad_shapes(self):
        """Non-object payloads, non-list rollups, and non-object entries are malformed."""
        for payload in ("[]", '{"statusCheckRollup": {"a": 1}}', '{"statusCheckRollup": [1]}'):
            with self.subTest(payload=payload):
                with self.assertRaises(MalformedResponseError):
                    parse_rollup(payload)
        self.assertEqual(parse_rollup('{"statusCheckRollup": null}'), [])


@patch("tools.pr_review.wait_for_ci.log_info", MagicMock())
@patch("tools.pr_review.wait_for_ci.subprocess.run")
class TestWaitForCI(unittest.TestCase):
    """Tests for the wait_for_ci polling loop."""

    def setUp(self):
        self.clock = FakeClock()
        self.args = argparse.Namespace(
            pr="148", repo="google/xmc", timeout=600, interval=10, log_lines=2
        )

    def _run(self):
        return wait_for_ci.wait_for_ci(self.args, sleep=self.clock.sleep, clock=self.clock.time)

    def test_success_after_pending_requires_stable_poll(self, mock_run):
        """Pending checks are polled; success needs two identical completed polls."""
        mock_run.side_effect = [
            _rollup(_check_run("lint", status="IN_PROGRESS", conclusion=None)),
            _rollup(_check_run("lint")),
            _rollup(_check_run("lint")),
        ]
        res = self._run()
        self.assertEqual(res["outcome"], "success")
        self.assertEqual(res["checks"], {PENDING: 0, PASSED: 1, FAILED: 0})
        self.assertEqual(self.clock.sleeps, [10, 20])
        mock_run.assert_called_with(
            ["gh", "pr", "view", "148", "--json", "statusCheckRollup", "--repo", "google/xmc"],
            capture_output=True,
            text=True,
            errors="replace",
            check=True,
            timeout=wait_for_ci.GH_COMMAND_TIMEOUT_SECONDS,
        )

    def test_late_registered_check_is_not_false_pass(self, mock_run):
        """A new check appearing after an all-green poll resets settlement."""
        mock_run.side_effect = [
            _rollup(_check_run("early")),
            _rollup(_check_run("early"), _check_run("late", status="QUEUED", conclusion=None)),
            _rollup(_check_run("early"), _check_run("late")),
            _rollup(_check_run("early"), _check_run("late")),
        ]
        self.assertEqual(self._run()["checks"][PASSED], 2)
        self.assertEqual(mock_run.call_count, 4)

    def test_no_checks_yet_then_success(self, mock_run):
        """An empty rollup is treated as pending, not success."""
        mock_run.side_effect = [_rollup(), _rollup(_check_run("a")), _rollup(_check_run("a"))]
        self.assertEqual(self._run()["outcome"], "success")

    def test_backoff_is_capped(self, mock_run):
        """Poll interval doubles but never exceeds MAX_INTERVAL_SECONDS."""
        self.args.timeout = 1000
        mock_run.return_value = _rollup()
        with self.assertRaises(HandledError):
            self._run()
        self.assertEqual(self.clock.sleeps[:5], [10, 20, 40, 80, 120])
        self.assertTrue(all(s <= wait_for_ci.MAX_INTERVAL_SECONDS for s in self.clock.sleeps))

    def test_timeout_with_no_checks(self, mock_run):
        """Timeout without any reported checks yields a dedicated message."""
        self.args.timeout = 25
        mock_run.return_value = _rollup()
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("No CI checks were reported", ctx.exception.message)
        self.assertEqual(ctx.exception.details, {"outcome": "timeout"})

    def test_timeout_uses_full_budget_with_final_poll_at_deadline(self, mock_run):
        """The last sleep is capped to the remaining time and a final poll runs."""
        self.args.timeout = 25
        mock_run.return_value = _rollup()
        with self.assertRaises(HandledError):
            self._run()
        self.assertEqual(self.clock.sleeps, [10, 15])
        self.assertEqual(self.clock.now, 25)
        self.assertEqual(mock_run.call_count, 3)

    def test_timeout_with_pending_checks(self, mock_run):
        """Timeout lists still-pending checks."""
        self.args.timeout = 25
        mock_run.return_value = _rollup(
            _check_run("slow", status="IN_PROGRESS", conclusion=None), _check_run("ok")
        )
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("Timed out", ctx.exception.message)
        self.assertEqual(_details(ctx)["pending_checks"], ["slow"])

    def test_failure_reports_checks_and_bounded_log_tail_per_job(self, mock_run):
        """Failures list run/job IDs and fetch one bounded log tail per failed job."""
        other_url = "https://github.com/google/xmc/actions/runs/222/job/1"
        failing = [
            _check_run("unit", conclusion="FAILURE"),
            _check_run("e2e", conclusion="TIMED_OUT", url=other_url),
            _check_run("lint"),
        ]
        mock_run.side_effect = [
            _rollup(*failing),
            _rollup(*failing),
            _completed("l1\nl2\n##[error]boom\nPost job cleanup."),
            _completed(returncode=1, stderr="HTTP 410"),
        ]
        with self.assertRaises(HandledError) as ctx:
            self._run()
        details = _details(ctx)
        self.assertEqual(details["outcome"], "failure")
        self.assertEqual(
            [(c["name"], c["run_id"], c["job_id"]) for c in details["failed_checks"]],
            [("unit", "111", "999"), ("e2e", "222", "1")],
        )
        self.assertEqual(details["failed_checks"][0]["log_tail"], "l2\n##[error]boom")
        self.assertIn("HTTP 410", details["failed_checks"][1]["log_tail"])
        mock_run.assert_any_call(
            ["gh", "api", "repos/google/xmc/actions/jobs/999/logs"],
            capture_output=True,
            text=True,
            errors="replace",
            check=False,
            timeout=wait_for_ci.GH_COMMAND_TIMEOUT_SECONDS,
        )
        self.assertEqual(mock_run.call_count, 4)

    def test_failure_without_actions_url_skips_logs(self, mock_run):
        """Non-Actions failures (e.g. StatusContext) are reported without logs."""
        entry = {"__typename": "StatusContext", "context": "cla", "state": "FAILURE"}
        mock_run.side_effect = [_rollup(entry), _rollup(entry)]
        with self.assertRaises(HandledError) as ctx:
            self._run()
        failed = _details(ctx)["failed_checks"][0]
        self.assertIsNone(failed["run_id"])
        self.assertIsNone(failed["log_tail"])
        self.assertEqual(mock_run.call_count, 2)

    def test_transient_gh_errors_are_retried(self, mock_run):
        """Isolated gh failures are retried and do not abort polling."""
        err = subprocess.CalledProcessError(1, "gh", stderr="HTTP 502")
        mock_run.side_effect = [err, _rollup(_check_run("a")), err, _rollup(_check_run("a"))]
        self.assertEqual(self._run()["outcome"], "success")

    def test_non_object_json_is_retried_as_transient(self, mock_run):
        """A non-object JSON response is retried like a transient gh error."""
        mock_run.side_effect = [
            _completed("[]"),
            _rollup(_check_run("a")),
            _rollup(_check_run("a")),
        ]
        self.assertEqual(self._run()["outcome"], "success")

    def test_repeated_non_object_json_raises(self, mock_run):
        """Persistent non-object JSON responses raise a HandledError."""
        mock_run.return_value = _completed("null")
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("expected a JSON object, got NoneType", ctx.exception.message)
        self.assertEqual(mock_run.call_count, wait_for_ci.MAX_CONSECUTIVE_GH_ERRORS)

    def test_repeated_gh_errors_raise(self, mock_run):
        """Consecutive gh failures beyond the limit raise a HandledError."""
        mock_run.side_effect = subprocess.CalledProcessError(1, "gh", stderr="not found")
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("not found", ctx.exception.message)
        self.assertEqual(mock_run.call_count, wait_for_ci.MAX_CONSECUTIVE_GH_ERRORS)

    def test_repo_flag_omitted_when_unset(self, mock_run):
        """Without --repo, gh resolves the repository from the current checkout."""
        self.args.repo = None
        mock_run.return_value = _rollup(_check_run("a"))
        self._run()
        self.assertNotIn("--repo", mock_run.call_args.args[0])

    def test_missing_gh_binary_raises_immediately(self, mock_run):
        """A missing gh binary is a non-retryable HandledError."""
        mock_run.side_effect = FileNotFoundError("gh")
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("gh CLI not found", ctx.exception.message)
        self.assertEqual(mock_run.call_count, 1)

    def test_gh_timeout_is_retried_as_transient(self, mock_run):
        """A hung gh call (TimeoutExpired) is retried like other transient errors."""
        hung = subprocess.TimeoutExpired("gh", wait_for_ci.GH_COMMAND_TIMEOUT_SECONDS)
        mock_run.side_effect = [hung, _rollup(_check_run("a")), _rollup(_check_run("a"))]
        self.assertEqual(self._run()["outcome"], "success")

    def test_malformed_rollup_entries_raise_after_retries(self, mock_run):
        """Persistent non-object rollup entries raise a HandledError after retries."""
        mock_run.return_value = _completed('{"statusCheckRollup": ["bad"]}')
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertIn("list of objects", ctx.exception.message)
        self.assertEqual(mock_run.call_count, wait_for_ci.MAX_CONSECUTIVE_GH_ERRORS)

    def test_job_log_fetch_errors_become_placeholders(self, mock_run):
        """Missing gh or a hung log download yields a placeholder, not a crash."""
        failing = _check_run("unit", conclusion="FAILURE")
        for error in (FileNotFoundError("gh"), subprocess.TimeoutExpired("gh", 60)):
            with self.subTest(error=type(error).__name__):
                mock_run.reset_mock()
                mock_run.side_effect = [_rollup(failing), _rollup(failing), error]
                with self.assertRaises(HandledError) as ctx:
                    self._run()
                log_tail = _details(ctx)["failed_checks"][0]["log_tail"]
                self.assertTrue(log_tail.startswith("<gh api job logs failed"))

    def test_deadline_reports_complete_but_unconfirmed_failure(self, mock_run):
        """At the deadline, a first fully-complete poll is reported, not timed out."""
        self.args.timeout = 25
        pending = _check_run("unit", status="IN_PROGRESS", conclusion=None)
        failed = _check_run("unit", conclusion="FAILURE")
        mock_run.side_effect = [
            _rollup(pending),
            _rollup(pending),
            _rollup(failed),
            _completed("##[error]boom"),
        ]
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertEqual(_details(ctx)["outcome"], "failure")
        self.assertEqual(self.clock.now, 25)

    def test_deadline_reports_complete_but_unconfirmed_success(self, mock_run):
        """At the deadline, an all-green first complete poll is reported as success."""
        self.args.timeout = 25
        pending = _check_run("unit", status="IN_PROGRESS", conclusion=None)
        mock_run.side_effect = [_rollup(pending), _rollup(pending), _rollup(_check_run("unit"))]
        self.assertEqual(self._run()["outcome"], "success")


if __name__ == "__main__":
    unittest.main()
