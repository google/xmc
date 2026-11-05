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

"""Unit tests for subprocess execution command helper."""

import os
import signal
import subprocess
import sys
import threading
import time
import unittest
from typing import Any
from unittest.mock import ANY, MagicMock, patch

from test.e2e.lib import cancellation, command
from test.e2e.lib.cancellation import HarnessStoppedError
from test.e2e.lib.command import run_cmd

# Upper bound for a killed command to return; far below its natural run time.
_KILL_BOUND_SECONDS = 5
# Process id of a mocked command.
_MOCK_PID = 4242


class TestRunCmd(unittest.TestCase):
    """Tests for run_cmd utility."""

    def setUp(self):
        self.controller = cancellation.StopController()
        patcher = patch.object(cancellation, "CONTROLLER", self.controller)
        patcher.start()
        self.addCleanup(patcher.stop)

    def _start_patch(self, patcher: Any) -> MagicMock:
        mock = patcher.start()
        self.addCleanup(patcher.stop)
        return mock

    def _mock_timed_out_command(self, killpg_error: OSError) -> tuple[MagicMock, MagicMock]:
        """Mocks a command that times out, and a process group kill that raises an error.

        Returns:
            The mocked process and the mocked ``os.killpg``.
        """
        popen = self._start_patch(patch.object(command.subprocess, "Popen"))
        killpg = self._start_patch(patch.object(command.os, "killpg", side_effect=killpg_error))
        popen.return_value.__exit__.return_value = False  # Propagate exceptions.
        process = popen.return_value.__enter__.return_value
        process.pid = _MOCK_PID
        process.communicate.side_effect = subprocess.TimeoutExpired("cmd", 0)
        return process, killpg

    def test_run_cmd_captures_output(self):
        res = run_cmd(
            ["sh", "-c", 'pwd; echo "$GREETING" >&2'],
            cwd="/",
            env={"GREETING": "hello"},
            capture_output=True,
        )

        self.assertEqual((res.returncode, res.stdout, res.stderr), (0, "/\n", "hello\n"))

    def test_run_cmd_raises_with_output_when_check_fails(self):
        with self.assertRaises(subprocess.CalledProcessError) as ctx:
            run_cmd(["sh", "-c", "echo boom >&2; exit 3"], capture_output=True)

        self.assertEqual((ctx.exception.returncode, ctx.exception.stderr), (3, "boom\n"))

    def test_run_cmd_returns_failed_process_without_check(self):
        res = run_cmd(["sh", "-c", "exit 3"], check=False)

        self.assertEqual((res.returncode, res.stdout), (3, None))

    def test_run_cmd_runs_command_in_its_own_session(self):
        res = run_cmd(
            [sys.executable, "-c", "import os; print(os.getsid(0) == os.getpid())"],
            capture_output=True,
        )

        self.assertEqual(res.stdout, "True\n")

    def test_run_cmd_kills_process_group_after_timeout(self):
        killpg = self._start_patch(patch.object(command.os, "killpg", wraps=os.killpg))
        start = time.monotonic()
        with self.assertRaises(subprocess.TimeoutExpired) as ctx:
            run_cmd(["sleep", "30"], timeout=0.2)

        self.assertEqual(ctx.exception.timeout, 0.2)
        self.assertLess(time.monotonic() - start, _KILL_BOUND_SECONDS)
        killpg.assert_called_once_with(ANY, signal.SIGKILL)

    def test_run_cmd_uses_default_timeout(self):
        clock = iter([0.0])
        with patch.object(command.time, "monotonic", side_effect=lambda: next(clock, 1e9)):
            with self.assertRaises(subprocess.TimeoutExpired) as ctx:
                run_cmd(["sleep", "30"])

        self.assertEqual(ctx.exception.timeout, 900.0)

    def test_run_cmd_kills_process_group_when_harness_halts(self):
        killpg = self._start_patch(patch.object(command.os, "killpg", wraps=os.killpg))
        threading.Timer(0.2, self.controller.abort_attempt).start()
        start = time.monotonic()
        with self.assertRaises(HarnessStoppedError):
            run_cmd(["sleep", "30"])

        self.assertLess(time.monotonic() - start, _KILL_BOUND_SECONDS)
        killpg.assert_called_once_with(ANY, signal.SIGKILL)

    def test_run_cmd_reaps_command_whose_process_group_already_exited(self):
        process, killpg = self._mock_timed_out_command(ProcessLookupError())

        with self.assertRaises(subprocess.TimeoutExpired):
            run_cmd(["cmd"], timeout=0)

        killpg.assert_called_once_with(_MOCK_PID, signal.SIGKILL)
        process.kill.assert_not_called()
        process.wait.assert_called_once_with()

    def test_run_cmd_kills_command_alone_when_process_group_kill_fails(self):
        process, _ = self._mock_timed_out_command(PermissionError())

        with self.assertLogs("xmc_e2e_runner", level="WARNING"):
            with self.assertRaises(subprocess.TimeoutExpired):
                run_cmd(["cmd"], timeout=0)

        process.kill.assert_called_once_with()
        process.wait.assert_called_once_with()

    def test_run_cmd_skips_command_when_harness_halted(self):
        self.controller.request_stop(signal.SIGTERM)
        with patch("subprocess.Popen") as popen, self.assertRaises(HarnessStoppedError):
            run_cmd(["true"])

        popen.assert_not_called()

    def test_run_cmd_runs_unstoppable_command_when_harness_halted(self):
        self.controller.request_stop(signal.SIGTERM)

        res = run_cmd(["sh", "-c", "sleep 0.7; echo done"], capture_output=True, stoppable=False)

        self.assertEqual(res.stdout, "done\n")


class TestCapturedText(unittest.TestCase):
    """Tests for captured_text."""

    def test_decodes_bytes_and_passes_text_through(self) -> None:
        cases: list[tuple[str | bytes | None, str]] = [
            (b"bytes out\n", "bytes out\n"),
            (b"bad \xff byte", "bad \ufffd byte"),
            ("text out\n", "text out\n"),
            (None, ""),
        ]
        for stream, want in cases:
            with self.subTest(stream=stream):
                self.assertEqual(command.captured_text(stream), want)


if __name__ == "__main__":
    unittest.main()
