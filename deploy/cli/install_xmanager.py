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

"""Installation and local proto compilation script for XManager CLI via pip.

Note: Because this script overlays compiled local `api/protos` directly into
`site-packages/xmanager_cloud` over the files shipped by the `xmanager` wheel,
it must be re-run after any `pip` operation (such as `pip install -U xmanager`
or `--force-reinstall`) that modifies `xmanager`.
"""

import argparse
import os
import shutil
import subprocess
import sys
import venv
from collections.abc import Mapping, Sequence
from pathlib import Path
from types import MappingProxyType

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_XMANAGER_SPEC = "xmanager>=0.8.5.post1"

# Upper bounds keep proto descriptor registration and protobuf runtime compatible:
# - googleapis-common-protos<1.75.4 avoids the 1.75.4 operations_proto.proto /
#   operations_proto_pb2 DescriptorPool registration conflict when importing
#   modules compiled against google/longrunning/operations.proto (see 941b7b7325).
# - protobuf<7.0.0 keeps the environment on protobuf 6.x (matching xmanager)
#   and prevents pip from selecting grpcio-tools releases that require
#   protobuf>=7.0.0.
RUNTIME_DEPENDENCIES = (
    "absl-py>=2.1.0",
    "google-auth>=2.28.0",
    "googleapis-common-protos>=1.63.0,<1.75.4",
    "grpcio>=1.62.0",
    "grpcio-tools>=1.62.0",
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
    "xdash",
    "xdash.auth",
)

# Local repository SDKs installed in editable mode, keyed by top-level import name.
# Each must resolve from its repository directory, never from a same-named copy
# bundled by another distribution (see verify_sdk_provenance).
LOCAL_SDKS: Mapping[str, Path] = MappingProxyType(
    {
        "xdash": Path("sdks") / "python" / "xdash",
        "xmc": Path("sdks") / "python" / "xmc",
    }
)

# Prints the origin file of the module named in argv[1], or an empty line if absent.
_MODULE_ORIGIN_SNIPPET = (
    "import importlib.util, sys; "
    "spec = importlib.util.find_spec(sys.argv[1]); "
    "print(spec.origin if spec and spec.origin else '')"
)


class SdkShadowedError(RuntimeError):
    """Raised when a local SDK resolves outside its repository directory."""


def error_exit(msg: str) -> None:
    """Print error message to stderr and exit.

    Args:
        msg: Error message to display.

    Raises:
        SystemExit: Always raised with status 1.
    """
    sys.stderr.write(f"Error: {msg}\n")
    sys.exit(1)


def resolve_xmanager_spec(version: str) -> str:
    """Resolve the pip package requirement specifier for xmanager.

    Args:
        version: Optional version tag, version specifier (e.g. '>=0.8.5.post1,<0.9'),
            or full PEP 508 requirement string.

    Returns:
        A pip requirement string such as 'xmanager==0.8.5.post1' or 'xmanager>=0.8.5.post1'.
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
            "Optional xmanager version (e.g. 0.8.5.post1), specifier (e.g. '>=0.8.5.post1,<0.9'), "
            "or full requirement (e.g. 'xmanager @ git+https://github.com/google-deepmind/xmanager@main'). "
            f"Defaults to XMANAGER_VERSION env var or '{DEFAULT_XMANAGER_SPEC}'."
        ),
    )
    parser.add_argument(
        "-r",
        "--requirements",
        action="append",
        default=[],
        metavar="PATH",
        help=(
            "Optional requirements file to install before applying xmanager and "
            "runtime dependency pins (repeatable)."
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


def install_xmanager_package(
    venv_python: Path,
    xmanager_spec: str,
    requirements: Sequence[str | Path] = (),
) -> None:
    """Upgrade pip, install optional requirements files, and install xmanager.

    Args:
        venv_python: Path to virtualenv python executable.
        xmanager_spec: Pip package requirement specifier for xmanager.
        requirements: Optional sequence of requirements.txt paths to install first.
    """
    sys.stdout.write("Upgrading pip...\n")
    subprocess.run(
        [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
        check=True,
    )
    for req_path in requirements:
        sys.stdout.write(f"Installing requirements from {req_path}...\n")
        subprocess.run(
            [str(venv_python), "-m", "pip", "install", "-r", str(req_path)],
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
    return Path(lines[0].strip()), Path(lines[1].strip())


def _ensure_longrunning_operations_proto(site_packages: Path) -> None:
    """Ensure google/longrunning/operations.proto exists in site-packages.

    Recent googleapis-common-protos releases (e.g. 1.75.3) ship only
    operations_proto.proto on disk while operations_proto_pb2 registers itself
    in the descriptor pool as google/longrunning/operations.proto.
    """
    ops_proto = site_packages / "google" / "longrunning" / "operations.proto"
    ops_src = site_packages / "google" / "longrunning" / "operations_proto.proto"
    if not ops_proto.exists() and ops_src.exists():
        shutil.copy2(ops_src, ops_proto)


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
        for stale in dst_dir.glob("*.proto"):
            stale.unlink()
        (dst_dir / "__init__.py").touch()
        for proto_file in sorted(src_dir.glob("*.proto")):
            dst_file = dst_dir / proto_file.name
            shutil.copy2(proto_file, dst_file)
            staged_files.append(str(dst_file))
    return staged_files


def cleanup_stale_bundled_packages(site_packages: Path) -> None:
    """Remove obsolete bundled packages from xmanager wheel that conflict with local repository SDKs.

    The upstream xmanager wheel bundles an outdated copy of `xdash` directly in
    site-packages that lacks recent SDK additions (such as `xdash.auth`). If left
    in place, standard import resolution loads this stale directory rather than
    local SDKs installed via editable mode or PYTHONPATH.
    """
    stale_xdash = site_packages / "xdash"
    if stale_xdash.is_dir() and not stale_xdash.is_symlink():
        sys.stdout.write(f"Removing obsolete bundled xdash from {stale_xdash}...\n")
        shutil.rmtree(stale_xdash)


def compile_local_protos(venv_python: Path, repo_root: Path = REPO_ROOT) -> None:
    """Compile local XMC API protos into the virtualenv's xmanager_cloud package.

    Args:
        venv_python: Path to virtualenv python executable.
        repo_root: Root directory of the XMC repository containing api/protos.
    """
    sys.stdout.write("Compiling local XMC protos from api/protos into virtualenv...\n")
    site_packages, grpc_proto_include = resolve_venv_proto_paths(venv_python)
    # DO NOT MERGE: purge disabled to reproduce the pre-#1482 shadowed-xdash venv.
    _ensure_longrunning_operations_proto(site_packages)
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


def install_local_sdks(venv_python: Path, repo_root: Path = REPO_ROOT) -> None:
    """Install local repository SDKs into the virtualenv in editable mode.

    Installs every `LOCAL_SDKS` directory present under repo_root with --no-deps in
    editable mode so the SDKs are available to users and callers of
    install_xmanager.py.

    Args:
        venv_python: Path to virtualenv python executable.
        repo_root: Root directory of the XMC repository containing sdks/.
    """
    for package, rel_dir in LOCAL_SDKS.items():
        sdk_dir = repo_root / rel_dir
        if not sdk_dir.is_dir():
            continue
        sys.stdout.write(f"Installing local {package} SDK from {sdk_dir} into virtualenv...\n")
        subprocess.run(
            [str(venv_python), "-m", "pip", "install", "--no-deps", "-e", str(sdk_dir)],
            check=True,
        )


def resolve_module_origin(venv_python: Path, module: str) -> Path | None:
    """Resolve the file a module would be loaded from inside the virtualenv.

    Runs in isolated mode (-I) so that PYTHONPATH and the current working
    directory cannot mask what the virtualenv itself resolves.

    Args:
        venv_python: Path to virtualenv python executable.
        module: Importable module name (e.g. "xdash").

    Returns:
        The resolved module origin path, or None if the module cannot be found.
    """
    result = subprocess.run(
        [str(venv_python), "-I", "-c", _MODULE_ORIGIN_SNIPPET, module],
        check=True,
        capture_output=True,
        text=True,
    )
    origin = result.stdout.strip()
    return Path(origin) if origin else None


def check_sdk_provenance(module: str, origin: Path | None, expected_dir: Path) -> None:
    """Ensure a module resolves from within its expected local SDK directory.

    Args:
        module: Importable module name.
        origin: Resolved module origin, or None if the module was not found.
        expected_dir: Directory the module must be loaded from.

    Raises:
        SdkShadowedError: If the module is missing or resolves outside expected_dir.
    """
    if origin is None:
        raise SdkShadowedError(f"{module} is not importable; expected it under {expected_dir}")
    if not origin.resolve().is_relative_to(expected_dir.resolve()):
        raise SdkShadowedError(
            f"{module} resolved to {origin}, expected under {expected_dir} "
            "(stale copy bundled by another distribution?)"
        )


def verify_sdk_provenance(venv_python: Path, repo_root: Path = REPO_ROOT) -> None:
    """Verify every installed local SDK is loaded from the repository checkout.

    Guards against another distribution (e.g. the xmanager wheel) shipping a
    same-named top-level package that silently shadows the editable install.

    Args:
        venv_python: Path to virtualenv python executable.
        repo_root: Root directory of the XMC repository containing sdks/.

    Raises:
        SdkShadowedError: If any present local SDK resolves outside repo_root.
    """
    sys.stdout.write("Verifying local SDK provenance...\n")
    for package, rel_dir in LOCAL_SDKS.items():
        expected_dir = repo_root / rel_dir
        if not expected_dir.is_dir():
            continue
        check_sdk_provenance(package, resolve_module_origin(venv_python, package), expected_dir)


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

    sys.stdout.write("=== Configuration ===\n")
    sys.stdout.write(f"XManager Package: {xmanager_spec}\n")
    sys.stdout.write(f"Local Protos:     {REPO_ROOT / 'api' / 'protos'}\n")
    sys.stdout.write(f"Venv Target:      {venv_dir}\n")
    sys.stdout.write("=====================\n")

    venv_python = setup_virtualenv(venv_dir)
    install_xmanager_package(venv_python, xmanager_spec, args.requirements)
    compile_local_protos(venv_python, REPO_ROOT)
    install_local_sdks(venv_python, REPO_ROOT)
    verify_imports(venv_python)
    try:
        pass  # DO NOT MERGE: provenance check disabled for proof run 2.
    except SdkShadowedError as err:
        error_exit(str(err))

    sys.stdout.write("==================================================\n")
    sys.stdout.write("Installation complete!\n")
    sys.stdout.write("To activate the virtual environment, run:\n")
    sys.stdout.write(f"  source {venv_dir}/bin/activate\n")
    sys.stdout.write("Then sign in to your XMC deployment as yourself:\n")
    sys.stdout.write("  xmanager login\n")
    sys.stdout.write("==================================================\n")


if __name__ == "__main__":
    main()
