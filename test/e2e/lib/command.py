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
import subprocess

DEFAULT_TIMEOUT_SECONDS: float = 900.0  # 15 minutes

logger = logging.getLogger("xmc_e2e_runner")


def run_cmd(
    cmd: list[str],
    cwd: str | None = None,
    env: dict[str, str] | None = None,
    timeout: float | None = DEFAULT_TIMEOUT_SECONDS,
    check: bool = True,
    capture_output: bool = False,
) -> subprocess.CompletedProcess[str]:
    """Runs a subprocess command with structured logging and timeout bounds."""
    logger.info("Executing: %s", " ".join(cmd))
    return subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        timeout=timeout,
        check=check,
        capture_output=capture_output,
        text=True,
    )
