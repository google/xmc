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

"""Utility module for formatting tool execution output and exceptions."""

import argparse
import json
import os
import subprocess
import sys
import traceback
from pathlib import Path
from typing import Optional


def _should_bypass_venv() -> bool:
    """Checks if virtual environment activation should be skipped."""
    if "TEST_SRCDIR" in os.environ or "BAZEL_TEST" in os.environ:
        return True
    if sys.prefix != sys.base_prefix or os.environ.get("XMC_BYPASS_VENV") == "1":
        return True
    if "unittest" in sys.modules or "pytest" in sys.modules:
        return True
    file_path_str = str(Path(__file__).resolve())
    cwd_path_str = str(Path.cwd().resolve())
    if any(
        token in file_path_str or token in cwd_path_str
        for token in ("execroot", "bazel-out", "sandbox")
    ):
        return True
    return False


def ensure_venv(venv_dir: Optional[Path] = None) -> None:
    """Ensures the current process runs inside the tooling virtual environment."""
    if _should_bypass_venv():
        return

    resolved_venv = (
        venv_dir
        or Path(
            os.environ.get("TOOLS_VENV_DIR", Path(__file__).resolve().parent / "venv")
        ).resolve()
    )
    venv_python = resolved_venv / "bin" / "python3"
    if not venv_python.exists():
        venv_python = resolved_venv / "bin" / "python"

    if not venv_python.exists():
        if not os.access(resolved_venv.parent, os.W_OK):
            return
        try:
            _bootstrap_venv(resolved_venv)
        except Exception:
            return
        venv_python = resolved_venv / "bin" / "python3"
        if not venv_python.exists():
            venv_python = resolved_venv / "bin" / "python"

    if venv_python.exists() and Path(sys.executable).resolve() != venv_python.resolve():
        os.execv(str(venv_python), [str(venv_python)] + sys.argv)


def _bootstrap_venv(venv_dir: Path) -> None:
    """Bootstraps the virtual environment if it does not yet exist."""
    setup_script = Path(__file__).resolve().parent / "setup_venv.py"
    if setup_script.is_file():
        subprocess.run(
            [sys.executable, str(setup_script), "--venv-dir", str(venv_dir)],
            check=True,
        )


class HandledError(Exception):
    """Exception raised for errors that are handled with instructions."""

    def __init__(self, message, instruction):
        super().__init__(message)
        self.message = message
        self.instruction = instruction


def run_tool(parser: argparse.ArgumentParser, main_fn):
    """Standard runner for tools.

    - Handles `--help` automatically via argparse.
    - Captures exceptions and formats output as JSON.
    - Exit codes:
      0: Success
      1: Handled error
      2: Unhandled error
    """
    ensure_venv()
    try:
        args = parser.parse_args()
        result = main_fn(args)

        output = {"status": "success", "result": result}
        print(json.dumps(output, indent=2))
        sys.exit(0)

    except HandledError as e:
        output = {
            "status": "error",
            "error_type": "handled",
            "message": e.message,
            "instruction": e.instruction,
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(1)

    except Exception as e:
        output = {
            "status": "error",
            "error_type": "unhandled",
            "message": str(e),
            "traceback": traceback.format_exc(),
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(2)
