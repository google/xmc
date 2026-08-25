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

"""Bootstrap script for XMC tooling Python virtual environment.

Requires Python 3.13.14 or newer.
"""

import argparse
import os
import shutil
import subprocess
import sys
import venv
from pathlib import Path

MINIMUM_PYTHON_VERSION = (3, 13, 14)
MINIMUM_PYTHON_VERSION_STR = ".".join(map(str, MINIMUM_PYTHON_VERSION))


def error_exit(msg: str) -> None:
    """Prints an error message to stderr and exits immediately."""
    sys.stderr.write(f"Error: {msg}\n")
    sys.exit(1)


def check_prerequisites() -> None:
    """Verifies python3 availability and minimum version requirement."""
    if not shutil.which("python3"):
        error_exit("python3 is required but not installed.")
    if sys.version_info[:3] < MINIMUM_PYTHON_VERSION:
        sys.stderr.write(
            "Warning: Virtual environments need to be created using Python"
            f" {MINIMUM_PYTHON_VERSION_STR} or newer.\n"
        )


def setup_virtualenv(venv_dir: Path) -> Path:
    """Creates the virtual environment if missing and returns the python binary path."""
    if not venv_dir.exists():
        sys.stdout.write(f"Creating tooling virtual environment at {venv_dir}...\n")
        venv.create(venv_dir, with_pip=True)

    venv_python = venv_dir / "bin" / "python3"
    if venv_python.exists():
        return venv_python
    return venv_dir / "bin" / "python"


def install_requirements(venv_python: Path, req_path: Path) -> None:
    """Upgrades pip and installs dependencies from requirements.txt."""
    if not req_path.is_file():
        error_exit(f"Requirements file not found at: {req_path}")

    sys.stdout.write("Upgrading pip and installing tooling dependencies...\n")
    try:
        subprocess.run(
            [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
            check=True,
        )
        subprocess.run(
            [str(venv_python), "-m", "pip", "install", "-r", str(req_path)],
            check=True,
        )
    except subprocess.CalledProcessError as e:
        sys.stderr.write(
            "\nError: Dependency installation failed.\n"
            "If packages are not found in your default index (e.g., corporate staging index),\n"
            "you can override or append index URLs using environment variables:\n\n"
            "  PIP_INDEX_URL=https://pypi.org/simple python3 tools/setup_venv.py\n"
            "  or\n"
            "  PIP_EXTRA_INDEX_URL=https://pypi.org/simple python3 tools/setup_venv.py\n\n"
            "Alternatively, ensure your global pip configuration (e.g., ~/.config/pip/pip.conf)\n"
            "points to a registry containing PyYAML, requests, and python-dotenv.\n"
        )
        sys.exit(e.returncode)


def main(args_list: list[str] | None = None) -> None:
    """Orchestrates virtual environment creation and dependency installation."""
    check_prerequisites()
    default_reqs = "./tools/common/requirements.txt"
    if not Path(default_reqs).exists() and Path("./tools/requirements.txt").exists():
        default_reqs = "./tools/requirements.txt"

    parser = argparse.ArgumentParser(
        description=(
            "Setup XMC tooling virtual environment (should be created using"
            f" Python {MINIMUM_PYTHON_VERSION_STR} or newer)."
        )
    )
    parser.add_argument(
        "--venv-dir",
        default=os.environ.get("TOOLS_VENV_DIR", "./tools/venv"),
        help="Target path for tooling virtual environment.",
    )
    parser.add_argument(
        "--requirements",
        default=default_reqs,
        help="Path to requirements.txt file.",
    )
    args = parser.parse_args(args_list)

    venv_dir = Path(args.venv_dir).resolve()
    req_path = Path(args.requirements).resolve()

    venv_python = setup_virtualenv(venv_dir)
    install_requirements(venv_python, req_path)
    sys.stdout.write(f"\nTooling virtual environment ready at: {venv_dir}\n")
    sys.stdout.write(f"To activate: source {venv_dir}/bin/activate\n")


if __name__ == "__main__":
    main()
