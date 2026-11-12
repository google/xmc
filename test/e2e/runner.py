#!/usr/bin/env python3
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

"""Entrypoint for XMC Non-Agentic Headless KinD E2E Testing.

Exit codes (see ``Outcome`` in ``lib/outcome.py``); none of them is retried externally:
    0: Every lifecycle phase passed.
    1: Product failure: the image build, the Helm deployment, or the tests failed.
    2: Invalid command-line arguments.
    3: Infra failure: creating the KinD cluster, loading images, or setting up Spanner
       failed, or the harness deadline expired. The harness already retries an infra
       failure once.
    4: Harness error: an unexpected error outside the lifecycle phases.
    128 + N: Stopped by signal N after a best-effort teardown.
"""

from __future__ import annotations

import logging
import os
import sys
from typing import TYPE_CHECKING

# Ensure workspace root is in sys.path when invoked directly as a script
_WORKSPACE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
_E2E_DIR = os.path.abspath(os.path.dirname(__file__))
if _WORKSPACE_ROOT not in sys.path:
    sys.path.insert(0, _WORKSPACE_ROOT)
if _E2E_DIR not in sys.path:
    sys.path.insert(0, _E2E_DIR)

if TYPE_CHECKING:
    from test.e2e.lib import cancellation
    from test.e2e.lib.config import parse_args
    from test.e2e.lib.infra_retry import run_attempt, run_with_infra_retry
    from test.e2e.lib.outcome import Outcome
    from test.e2e.lib.step_summary import append_step_summary
else:
    try:
        from test.e2e.lib import cancellation
        from test.e2e.lib.config import parse_args
        from test.e2e.lib.infra_retry import run_attempt, run_with_infra_retry
        from test.e2e.lib.outcome import Outcome
        from test.e2e.lib.step_summary import append_step_summary
    except ImportError:
        from lib import cancellation
        from lib.config import parse_args
        from lib.infra_retry import run_attempt, run_with_infra_retry
        from lib.outcome import Outcome
        from lib.step_summary import append_step_summary

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger("xmc_e2e_runner")


def main() -> int:
    """CLI entrypoint for running the E2E test suite.

    Returns:
        The process exit code documented in the module docstring.
    """
    try:
        cfg = parse_args()
        cancellation.CONTROLLER.install_signal_handlers()
        result = run_with_infra_retry(cfg, run_attempt)
    except Exception:
        logger.exception("E2E harness error outside the lifecycle phases")
        return int(Outcome.HARNESS_ERROR)
    append_step_summary(result)
    return int(result.outcome)


if __name__ == "__main__":
    sys.exit(main())
