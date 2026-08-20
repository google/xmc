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

"""Playwright virtualenv management and headless E2E test execution."""

from __future__ import annotations

import logging
import os
import sys

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


class PlaywrightTestExecutor:
    """Executes the Playwright headless test script inside a virtual environment."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.venv_dir = os.path.join(self.cfg.workspace_root, ".venv_e2e")

    def setup_venv(self) -> str:
        """Sets up Python virtual environment and installs dependencies."""
        python_bin = sys.executable
        if not os.path.exists(os.path.join(self.venv_dir, "bin", "python")):
            logger.info("Creating Python virtualenv at %s...", self.venv_dir)
            run_cmd([python_bin, "-m", "venv", self.venv_dir])
            run_cmd(
                [
                    os.path.join(self.venv_dir, "bin", "pip"),
                    "install",
                    "--upgrade",
                    "pip",
                    "playwright",
                    "requests",
                ]
            )
            run_cmd(
                [
                    os.path.join(self.venv_dir, "bin", "playwright"),
                    "install",
                    "chromium",
                ]
            )
        return os.path.join(self.venv_dir, "bin", "python")

    def execute(self) -> None:
        """Runs the test_e2e_flow.py script."""
        logger.info("=== [Playwright] Executing E2E Test Flow ===")
        py_venv = self.setup_venv()
        test_script = os.path.join(self.cfg.workspace_root, "test", "e2e", "test_e2e_flow.py")
        env = os.environ.copy()
        env["XMC_BASE_URL"] = "http://localhost:8080"
        env["XMC_DEX_URL"] = "http://localhost:5556"
        env["XMC_SCREENSHOTS_DIR"] = self.cfg.screenshots_dir
        existing_pythonpath = env.get("PYTHONPATH", "")
        env["PYTHONPATH"] = (
            f"{self.cfg.workspace_root}:{existing_pythonpath}"
            if existing_pythonpath
            else self.cfg.workspace_root
        )

        run_cmd([py_venv, test_script], cwd=self.cfg.workspace_root, env=env)


# Compatibility alias
TestExecutor = PlaywrightTestExecutor
