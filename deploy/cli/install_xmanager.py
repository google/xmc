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

"""Installation and virtual environment setup script for XManager CLI via pip."""

import argparse
import os
import shutil
import subprocess
import sys
import venv
from pathlib import Path

DEFAULT_XMANAGER_SPEC = "xmanager>=0.8.2"

RUNTIME_DEPENDENCIES = (
    "absl-py>=2.1.0",
    "google-auth>=2.28.0",
    "googleapis-common-protos>=1.63.0,<1.75.4",
    "grpcio>=1.62.0",
    "protobuf>=4.25.0",
    "python-dotenv>=1.0.0",
    "requests>=2.32.0",
)

VERIFIED_MODULES = (
    "xmanager",
    "xmanager.xm_cloud",
    "xmanager_cloud",
    "xmanager_cloud.experiment_state_server.experiment_state_api",
    "xmanager_cloud.experiment_state_server.proto.api_pb2",
    "xmanager_cloud.dashboard_service.proto.messages_pb2",
)


def error_exit(msg: str) -> None:
    """Print error message to stderr and exit.

    Args:
        msg: Error message to display.

    Raises:
        SystemExit: Always raised with status 1.
    """
    sys.stderr.write(f"Error: {msg}\n")
    sys.exit(1)


def check_prerequisites() -> None:
    """Verify python3 binary is installed.

    Raises:
        SystemExit: If python3 binary is not found in PATH.
    """
    if not shutil.which("python3"):
        error_exit("python3 is required but not installed.")


def resolve_xmanager_spec(version: str) -> str:
    """Resolve the pip package requirement specifier for xmanager.

    Args:
        version: Optional version tag string (e.g., '0.8.2' or '0.8.4').

    Returns:
        A pip requirement string such as 'xmanager==0.8.2' or 'xmanager>=0.8.2'.
    """
    cleaned = version.strip()
    if not cleaned:
        return DEFAULT_XMANAGER_SPEC
    if cleaned.startswith("xmanager"):
        return cleaned
    if cleaned.startswith(("==", ">=", "<=", "~=", "!=", ">", "<")):
        return f"xmanager{cleaned}"
    return f"xmanager=={cleaned}"


def parse_args(args: list[str] | None = None) -> argparse.Namespace:
    """Parse command line arguments.

    Args:
        args: Optional raw argument list.

    Returns:
        Parsed arguments Namespace.
    """
    default_venv = os.environ.get("VENV_DIR", "./venv")
    default_version = os.environ.get("XMANAGER_VERSION", "")

    parser = argparse.ArgumentParser(
        description="Install XManager CLI and XMC runtime dependencies from PyPI."
    )
    parser.add_argument(
        "--venv",
        default=default_venv,
        help="Virtual environment target directory (default: ./venv)",
    )
    parser.add_argument(
        "--xmanager-version",
        default=default_version,
        help=(
            "Optional XManager PyPI version tag to pin (e.g. 0.8.2). "
            f"Defaults to XMANAGER_VERSION env var or '{DEFAULT_XMANAGER_SPEC}'."
        ),
    )
    return parser.parse_args(args)


def setup_virtualenv(venv_dir: Path) -> Path:
    """Create virtual environment if it does not exist.

    Args:
        venv_dir: Path to virtual environment directory.

    Returns:
        Path to python executable inside virtual environment.
    """
    venv_python = venv_dir / "bin" / "python3"
    if not venv_python.exists() and not (venv_dir / "bin" / "python").exists():
        sys.stdout.write(f"Creating virtual environment at {venv_dir}...\n")
        venv.create(venv_dir, with_pip=True)

    if not venv_python.exists():
        venv_python = venv_dir / "bin" / "python"
    return venv_python


def install_xmanager_package(venv_python: Path, xmanager_spec: str) -> None:
    """Upgrade pip and install xmanager and runtime dependencies from PyPI.

    Args:
        venv_python: Path to virtualenv python executable.
        xmanager_spec: Pip package requirement specifier for xmanager.
    """
    sys.stdout.write("Upgrading pip...\n")
    subprocess.run(
        [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
        check=True,
    )
    sys.stdout.write(f"Installing {xmanager_spec} and runtime dependencies via pip...\n")
    subprocess.run(
        [
            str(venv_python),
            "-m",
            "pip",
            "install",
            xmanager_spec,
            *RUNTIME_DEPENDENCIES,
        ],
        check=True,
    )


def verify_imports(venv_python: Path) -> None:
    """Verify that xmanager and bundled xmanager_cloud modules import cleanly.

    Args:
        venv_python: Path to virtualenv python executable.
    """
    sys.stdout.write("Verifying imports...\n")
    for mod in VERIFIED_MODULES:
        subprocess.run(
            [str(venv_python), "-c", f"import {mod}"],
            check=True,
        )


def main(raw_args: list[str] | None = None) -> None:
    """Main execution function for install_xmanager script.

    Args:
        raw_args: Optional CLI arguments list for parameter overrides.
    """
    args = parse_args(raw_args)
    venv_dir = Path(args.venv).resolve()
    xmanager_spec = resolve_xmanager_spec(args.xmanager_version)

    check_prerequisites()

    sys.stdout.write("=== Configuration ===\n")
    sys.stdout.write(f"XManager Package: {xmanager_spec}\n")
    sys.stdout.write(f"Venv Target:      {venv_dir}\n")
    sys.stdout.write("=====================\n")

    venv_python = setup_virtualenv(venv_dir)
    install_xmanager_package(venv_python, xmanager_spec)
    verify_imports(venv_python)

    sys.stdout.write("==================================================\n")
    sys.stdout.write("Installation complete!\n")
    sys.stdout.write("To activate the virtual environment, run:\n")
    sys.stdout.write(f"  source {venv_dir}/bin/activate\n")
    sys.stdout.write("==================================================\n")


if __name__ == "__main__":
    main()
