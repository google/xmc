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

"""Subprocess execution utilities for E2E orchestration."""

from __future__ import annotations

import logging
import os
import signal
import subprocess
import time

from test.e2e.lib import cancellation

DEFAULT_TIMEOUT_SECONDS: float = 900.0  # 15 minutes
# Timeout of a teardown command. Teardown commands still run after a stop, so this bounds
# how long teardown can take.
TEARDOWN_TIMEOUT_SECONDS: float = 20.0
# How often a running command checks whether the harness halted.
_HALT_POLL_SECONDS = 0.5

logger = logging.getLogger("xmc_e2e_runner")


def run_cmd(
    cmd: list[str],
    cwd: str | None = None,
    env: dict[str, str] | None = None,
    timeout: float | None = DEFAULT_TIMEOUT_SECONDS,
    check: bool = True,
    capture_output: bool = False,
    stoppable: bool = True,
) -> subprocess.CompletedProcess[str]:
    """Runs a subprocess command with structured logging and timeout bounds.

    The command runs in its own session, so that killing it also kills the processes that it
    started.

    Args:
        cmd: The command and its arguments.
        cwd: Working directory of the command.
        env: Environment of the command; None inherits the harness environment.
        timeout: Seconds after which the command is killed; None waits indefinitely.
        check: Whether a non-zero exit status raises ``CalledProcessError``.
        capture_output: Whether to capture stdout and stderr as text.
        stoppable: Whether a halted harness skips or kills the command. Teardown commands
            pass False so that they still run after a stop.

    Returns:
        The finished process.

    Raises:
        HarnessStoppedError: If the command is stoppable and the harness halted.
        subprocess.TimeoutExpired: If the command was killed after ``timeout``.
        subprocess.CalledProcessError: If ``check`` is set and the command failed.
    """
    if stoppable:
        cancellation.CONTROLLER.raise_if_halted()
    logger.info("Executing: %s", " ".join(cmd))
    pipe = subprocess.PIPE if capture_output else None
    with subprocess.Popen(
        cmd, cwd=cwd, env=env, stdout=pipe, stderr=pipe, text=True, start_new_session=True
    ) as process:
        try:
            stdout, stderr = _communicate(process, timeout, stoppable)
        except BaseException:
            _kill_process_group(process)
            raise
    if check and process.returncode:
        raise subprocess.CalledProcessError(process.returncode, cmd, stdout, stderr)
    return subprocess.CompletedProcess(cmd, process.returncode, stdout, stderr)


def _kill_process_group(process: subprocess.Popen[str]) -> None:
    """Kills the process group that ``process`` leads, then reaps ``process``.

    Processes that left the group, such as daemons that start their own session, survive.
    """
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass  # Every process of the group has exited.
    except OSError as error:
        logger.warning("Could not kill process group %d: %s", process.pid, error)
        process.kill()
    process.wait()


def _communicate(
    process: subprocess.Popen[str], timeout: float | None, stoppable: bool
) -> tuple[str | None, str | None]:
    """Waits for ``process`` to exit and returns its output, checking for a halt meanwhile."""
    deadline = None if timeout is None else time.monotonic() + timeout
    while True:
        poll_seconds = _HALT_POLL_SECONDS
        if deadline is not None:
            poll_seconds = max(0.0, min(poll_seconds, deadline - time.monotonic()))
        try:
            return process.communicate(timeout=poll_seconds)
        except subprocess.TimeoutExpired as exc:
            if stoppable:
                cancellation.CONTROLLER.raise_if_halted()
            if deadline is not None and time.monotonic() >= deadline:
                raise subprocess.TimeoutExpired(
                    process.args, timeout, exc.output, exc.stderr
                ) from None
