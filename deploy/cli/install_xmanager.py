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

"""Installation and local proto compilation script for XManager CLI via pip."""

import argparse
import os
import shutil
import subprocess
import sys
import venv
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_XMANAGER_SPEC = "xmanager>=0.8.2"

RUNTIME_DEPENDENCIES = (
    "absl-py>=2.1.0",
    "google-auth>=2.28.0",
    "googleapis-common-protos>=1.63.0,<1.75.4",
    "grpcio>=1.62.0",
    "grpcio-tools>=1.62.0,<1.79.0",
    "protobuf>=4.25.0,<7.0.0",
    "python-dotenv>=1.0.0",
    "requests>=2.32.0",
)

PROTO_PACKAGE_DIRS = (
    ("experiment", "experiment_state_server"),
    ("xid", "xid_service"),
    ("dashboard", "dashboard_service"),
)

VERIFIED_MODULES = (
    "xmanager",
    "xmanager.xm_cloud",
    "xmanager_cloud",
    "xmanager_cloud.experiment_state_server.experiment_state_api",
    "xmanager_cloud.experiment_state_server.proto.api_pb2",
    "xmanager_cloud.xid_service.proto.api_pb2",
    "xmanager_cloud.dashboard_service.proto.dashboard_service_pb2",
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
        version: Optional version tag or specifier string (e.g., '0.8.2').

    Returns:
        A pip requirement string such as 'xmanager==0.8.2' or 'xmanager>=0.8.2'.
    """
    cleaned = version.strip()
    if not cleaned:
        return DEFAULT_XMANAGER_SPEC
    if cleaned.lower().startswith("xmanager"):
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
        description=(
            "Install XManager CLI from PyPI and compile local XMC API protos "
            "into the virtual environment."
        )
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


def resolve_venv_proto_paths(venv_python: Path) -> tuple[Path, Path]:
    """Resolve site-packages and grpc_tools proto include paths in the virtualenv.

    Args:
        venv_python: Path to virtualenv python executable.

    Returns:
        Tuple of (site_packages_dir, grpc_proto_include_dir).
    """
    probe_code = (
        "import grpc_tools, pathlib, sysconfig; "
        "print(sysconfig.get_paths()['purelib']); "
        "print(pathlib.Path(grpc_tools.__file__).parent / '_proto')"
    )
    res = subprocess.run(
        [str(venv_python), "-c", probe_code],
        check=True,
        capture_output=True,
        text=True,
    )
    lines = res.stdout.strip().splitlines()
    site_packages = Path(lines[0].strip())
    grpc_proto_include = Path(lines[1].strip())

    ops_proto = site_packages / "google" / "longrunning" / "operations.proto"
    ops_src = site_packages / "google" / "longrunning" / "operations_proto.proto"
    if not ops_proto.exists() and ops_src.exists():
        shutil.copy2(ops_src, ops_proto)

    return site_packages, grpc_proto_include


def stage_local_protos(repo_root: Path, site_packages: Path) -> list[str]:
    """Copy local XMC .proto files into site-packages/xmanager_cloud.

    Args:
        repo_root: Root directory of the XMC repository.
        site_packages: Target virtualenv site-packages directory.

    Returns:
        Sorted list of staged .proto file paths to compile.
    """
    staged_files: list[str] = []
    for src_subdir, dst_pkg in PROTO_PACKAGE_DIRS:
        src_dir = repo_root / "api" / "protos" / src_subdir
        if not src_dir.exists():
            error_exit(f"Local proto directory not found: {src_dir}")
        dst_dir = site_packages / "xmanager_cloud" / dst_pkg / "proto"
        dst_dir.mkdir(parents=True, exist_ok=True)
        (dst_dir / "__init__.py").touch()
        for proto_file in sorted(src_dir.glob("*.proto")):
            dst_file = dst_dir / proto_file.name
            shutil.copy2(proto_file, dst_file)
            staged_files.append(str(dst_file))
    return staged_files


def compile_local_protos(venv_python: Path, repo_root: Path = REPO_ROOT) -> None:
    """Compile local XMC API protos into the virtualenv's xmanager_cloud package.

    Args:
        venv_python: Path to virtualenv python executable.
        repo_root: Root directory of the XMC repository containing api/protos.
    """
    sys.stdout.write("Compiling local XMC protos from api/protos into virtualenv...\n")
    site_packages, grpc_proto_include = resolve_venv_proto_paths(venv_python)
    proto_files = stage_local_protos(repo_root, site_packages)
    subprocess.run(
        [
            str(venv_python),
            "-m",
            "grpc_tools.protoc",
            f"-I{site_packages}",
            f"-I{grpc_proto_include}",
            f"--python_out={site_packages}",
            f"--grpc_python_out={site_packages}",
            *proto_files,
        ],
        check=True,
    )


def verify_imports(venv_python: Path) -> None:
    """Verify that xmanager and compiled xmanager_cloud modules import cleanly.

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
    sys.stdout.write(f"Local Protos:     {REPO_ROOT / 'api' / 'protos'}\n")
    sys.stdout.write(f"Venv Target:      {venv_dir}\n")
    sys.stdout.write("=====================\n")

    venv_python = setup_virtualenv(venv_dir)
    install_xmanager_package(venv_python, xmanager_spec)
    compile_local_protos(venv_python, REPO_ROOT)
    verify_imports(venv_python)

    sys.stdout.write("==================================================\n")
    sys.stdout.write("Installation complete!\n")
    sys.stdout.write("To activate the virtual environment, run:\n")
    sys.stdout.write(f"  source {venv_dir}/bin/activate\n")
    sys.stdout.write("Then sign in to your XMC deployment as yourself:\n")
    sys.stdout.write("  xmanager login\n")
    sys.stdout.write("==================================================\n")


if __name__ == "__main__":
    main()
