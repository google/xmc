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

"""Bootstrap script for XMC tooling Python virtual environment."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
from typing import Optional
import venv


def error_exit(msg: str) -> None:
  """Prints an error message to stderr and exits immediately."""
  sys.stderr.write(f"Error: {msg}\n")
  sys.exit(1)


def check_prerequisites() -> None:
  """Verifies that python3 executable is available."""
  if not shutil.which("python3"):
    error_exit("python3 is required but not installed.")


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
  subprocess.run(
      [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
      check=True,
  )
  subprocess.run(
      [str(venv_python), "-m", "pip", "install", "-r", str(req_path)],
      check=True,
  )


def main(args_list: Optional[list[str]] = None) -> None:
  """Orchestrates virtual environment creation and dependency installation."""
  check_prerequisites()
  parser = argparse.ArgumentParser(
      description="Setup XMC tooling virtual environment."
  )
  parser.add_argument(
      "--venv-dir",
      default=os.environ.get("TOOLS_VENV_DIR", "./tools/venv"),
      help="Target path for tooling virtual environment.",
  )
  parser.add_argument(
      "--requirements",
      default="./tools/requirements.txt",
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
