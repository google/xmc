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

"""Entrypoint for XMC Non-Agentic Headless KinD E2E Testing."""

from __future__ import annotations

import logging
import os
import sys

# Ensure workspace root is in sys.path when invoked directly as a script
_WORKSPACE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
_E2E_DIR = os.path.abspath(os.path.dirname(__file__))
if _WORKSPACE_ROOT not in sys.path:
    sys.path.insert(0, _WORKSPACE_ROOT)
if _E2E_DIR not in sys.path:
    sys.path.insert(0, _E2E_DIR)

try:
    from test.e2e.lib.config import parse_args  # noqa: E402
    from test.e2e.lib.orchestrator import E2EOrchestrator  # noqa: E402
except ImportError:
    from lib.config import parse_args  # noqa: E402
    from lib.orchestrator import E2EOrchestrator  # noqa: E402

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)


def main() -> None:
    """CLI entrypoint for running the E2E test suite."""
    cfg = parse_args()
    orchestrator = E2EOrchestrator(cfg)
    orchestrator.run()


if __name__ == "__main__":
    main()
