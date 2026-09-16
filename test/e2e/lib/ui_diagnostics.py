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

"""UI diagnostic screenshot capture and trace logging for E2E tests."""

from __future__ import annotations

import logging
import os
import tempfile
from pathlib import Path
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from playwright.sync_api import Page

logger = logging.getLogger(__name__)


def get_screenshots_dir() -> Path:
    """Returns the resolved screenshots and diagnostic logs output directory.

    Returns:
        Path pointing to the output directory.
    """
    env_dir = os.getenv("XMC_SCREENSHOTS_DIR")
    if env_dir:
        screenshots_path = Path(env_dir)
    else:
        screenshots_path = Path(tempfile.gettempdir()) / "xmc_e2e_screenshots"
    screenshots_path.mkdir(parents=True, exist_ok=True)
    return screenshots_path


def setup_logging(screenshots_dir: Path) -> logging.Handler:
    """Configures console and file logging handlers for test execution.

    Args:
        screenshots_dir: Directory where diagnostic trace files will be saved.

    Returns:
        The configured file handler instance.
    """
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s [%(levelname)s] %(message)s",
    )
    trace_path = screenshots_dir / "playwright_e2e_trace.txt"
    trace_handler = logging.FileHandler(trace_path, mode="w", encoding="utf-8")
    trace_handler.setFormatter(logging.Formatter("%(asctime)s [%(levelname)s] %(message)s"))
    logging.getLogger().addHandler(trace_handler)
    return trace_handler


def _capture_screenshot_target(
    target: Page | Any,
    screenshots_dir: Path,
    name: str,
    target_type: str = "screenshot",
    **screenshot_kwargs: Any,
) -> str:
    """Internal helper to capture and save a screenshot from a Page or Locator target."""
    try:
        screenshots_dir.mkdir(parents=True, exist_ok=True)
        img_path = screenshots_dir / f"{name}.png"
        target.screenshot(path=str(img_path), **screenshot_kwargs)
        logger.info("📸 Saved %s: %s", target_type, img_path)
        return str(img_path)
    except Exception as e:
        logger.warning("Failed to save %s %s: %s", target_type, name, e)
        return ""


def save_screenshot(page: Page | Any, screenshots_dir: Path, name: str) -> str:
    """Captures and saves a full-page screenshot for diagnostic logs.

    Args:
        page: Active Playwright Page instance (or duck-typed mock).
        screenshots_dir: Destination directory.
        name: Base filename prefix without extension.

    Returns:
        String path to the saved screenshot, or empty string on failure.
    """
    return _capture_screenshot_target(
        page, screenshots_dir, name, target_type="screenshot", full_page=True
    )


def save_element_screenshot(element: Any, screenshots_dir: Path, name: str) -> str:
    """Captures and saves an element screenshot for diagnostic logs.

    Args:
        element: Active Playwright Locator instance.
        screenshots_dir: Destination directory.
        name: Base filename prefix without extension.

    Returns:
        String path to the saved screenshot, or empty string on failure.
    """
    return _capture_screenshot_target(
        element, screenshots_dir, name, target_type="element screenshot"
    )
