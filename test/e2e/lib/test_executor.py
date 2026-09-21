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
import subprocess
import sys

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig, E2ETier
from test.e2e.lib.dev_token import fetch_dex_oidc_token

logger = logging.getLogger("xmc_e2e_runner")


class PlaywrightTestExecutor:
    """Executes the Playwright headless test script inside a virtual environment."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.venv_dir = os.path.join(self.cfg.temp_dir, "venv_e2e")

    def _ensure_xmanager_installed(self, target_python: str) -> None:
        """Installs xmanager with xm_cloud into the target environment for workload tier."""
        if self.cfg.tier != E2ETier.WORKLOAD:
            return
        install_script = os.path.join(
            self.cfg.workspace_root, "deploy", "cli", "install_xmanager.py"
        )
        if os.path.exists(install_script):
            try:
                run_cmd([target_python, "-c", "import xmanager.xm_cloud"])
            except (subprocess.CalledProcessError, FileNotFoundError):
                logger.info("Setting up xmanager with xm_cloud in %s...", self.venv_dir)
                run_cmd([sys.executable, install_script, "--venv", self.venv_dir])

    def setup_venv(self) -> str:
        """Sets up Python virtual environment and installs dependencies, or uses pre-baked environment."""
        # 1. Proactively discover and export pre-baked browser binaries so either execution
        # path (pre-baked system Python or fallback .venv_e2e) avoids downloading Chromium.
        if "PLAYWRIGHT_BROWSERS_PATH" not in os.environ:
            browser_candidates = [
                "/ms-playwright",
                os.path.expanduser("~/.cache/ms-playwright"),
                os.path.expanduser("~/Library/Caches/ms-playwright"),
                os.path.expanduser("~/AppData/Local/ms-playwright"),
            ]
            for path in browser_candidates:
                try:
                    if os.path.isdir(path) and os.listdir(path):
                        os.environ["PLAYWRIGHT_BROWSERS_PATH"] = path
                        logger.info("Discovered pre-baked Playwright browsers at %s.", path)
                        break
                except OSError:
                    continue

        # 2. Fast Path: Use system Python directly if playwright and requests are already installed
        # and pre-baked browser binaries are present (0s setup).
        if (
            os.environ.get("GITHUB_ACTIONS") == "true"
            or os.environ.get("XMC_USE_PREBAKED_ENV") == "true"
            or os.environ.get("PLAYWRIGHT_BROWSERS_PATH")
        ):
            try:
                import playwright  # noqa: F401
                import requests  # noqa: F401

                if self.cfg.tier == E2ETier.WORKLOAD:
                    import absl  # noqa: F401
                    import dotenv  # noqa: F401
                    import google.auth  # noqa: F401
                    import grpc  # noqa: F401
                    import xmanager.xm_cloud  # noqa: F401

                if os.environ.get("PLAYWRIGHT_BROWSERS_PATH"):
                    logger.info(
                        "Using pre-baked Playwright environment at %s (0s setup).",
                        sys.executable,
                    )
                    return sys.executable
            except Exception as e:
                logger.debug(
                    "Pre-baked Python missing required packages (%s); falling back to virtualenv.",
                    e,
                )

        python_bin = sys.executable
        venv_python = os.path.join(self.venv_dir, "bin", "python")
        if not os.path.exists(venv_python):
            logger.info("Creating Python virtualenv at %s...", self.venv_dir)
            run_cmd([python_bin, "-m", "venv", self.venv_dir])
            run_cmd(
                [
                    os.path.join(self.venv_dir, "bin", "pip"),
                    "install",
                    "--upgrade",
                    "pip",
                    *self.cfg.venv_dependencies,
                ]
            )
            run_cmd(
                [
                    os.path.join(self.venv_dir, "bin", "playwright"),
                    "install",
                    "chromium",
                ]
            )

        self._ensure_xmanager_installed(venv_python)
        return venv_python

    def execute(self, test_script: str | None = None) -> None:
        """Runs the Playwright E2E test script."""
        logger.info("=== [Playwright] Executing E2E Test Flow ===")
        py_venv = self.setup_venv()
        resolved_test_script = (
            test_script
            or self.cfg.test_script
            or os.path.join(self.cfg.workspace_root, "test", "e2e", "test_e2e_flow.py")
        )
        env = os.environ.copy()
        env["XMC_BASE_URL"] = "http://localhost:8080"
        env["XMC_DEX_URL"] = "http://localhost:5556"
        env["XMC_SCREENSHOTS_DIR"] = self.cfg.screenshots_dir
        env["KUBE_CONTEXT"] = self.cfg.kube_context
        if "XMC_AUTH_TOKEN" not in env:
            env["XMC_AUTH_TOKEN"] = fetch_dex_oidc_token()
        existing_pythonpath = env.get("PYTHONPATH", "")
        env["PYTHONPATH"] = (
            f"{self.cfg.workspace_root}{os.pathsep}{existing_pythonpath}"
            if existing_pythonpath
            else self.cfg.workspace_root
        )

        run_cmd([py_venv, resolved_test_script], cwd=self.cfg.workspace_root, env=env)


# Compatibility alias
TestExecutor = PlaywrightTestExecutor
