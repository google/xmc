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

"""Installation and setup script for XManager CLI."""

import argparse
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import venv
from pathlib import Path

MIN_GIT_VERSION = (2, 25)
DASHBOARD_BAZEL_TARGETS = (
    "//api/protos/dashboard:dashboard_messages_py_proto",
    "//api/protos/dashboard:dashboard_service_py_proto",
)
DASHBOARD_VIRTUAL_IMPORT_TARGETS = (
    "dashboard_messages_proto",
    "dashboard_service_proto",
)
# TODO: pin to a release tag once xmc publishes releases.
REMOTE_XMC_SDK_LOC = "git+https://github.com/google/xmc.git@main#subdirectory=sdks/python/xmc"


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
    """Verify git and python3 binaries are installed.

    Raises:
        SystemExit: If any required binary is not found.
    """
    for cmd in ("git", "python3"):
        if not shutil.which(cmd):
            error_exit(f"{cmd} is required but not installed.")


def check_git_version() -> None:
    """Check Git version for sparse-checkout support (Git 2.25+)."""
    try:
        res = subprocess.run(
            ["git", "--version"],
            capture_output=True,
            text=True,
            check=True,
        )
        match = re.search(r"(\d+)\.(\d+)(?:\.\d+)?", res.stdout)
        if not match:
            raise ValueError("Could not find version pattern")
        major, minor = int(match.group(1)), int(match.group(2))
        version_str = match.group(0)
    except (subprocess.SubprocessError, IndexError, ValueError):
        sys.stderr.write("Warning: Could not parse Git version. Git 2.25+ is recommended.\n")
        return

    min_major, min_minor = MIN_GIT_VERSION
    if major < min_major or (major == min_major and minor < min_minor):
        sys.stderr.write(
            f"Warning: Git version is {version_str}. "
            "Git 2.25+ is required for automatic sparse checkout.\n"
            "If downloading remote protos fails, please upgrade Git "
            "or download the protos manually and use "
            "--protos-loc <local_path>.\n"
        )


def parse_github_url(url: str) -> tuple[str, str, str]:
    """Parse standard GitHub tree URLs into repo URL, branch, and subdir.

    Args:
        url: GitHub URL string.

    Returns:
        A tuple of (repo_url, branch, subdir).

    Raises:
        SystemExit: If the URL format is invalid.
    """
    pattern = r"^(https?://github\.com/[^/]+/[^/]+)/tree/([^/]+)/(.*)$"
    match = re.match(pattern, url)
    if match:
        return f"{match.group(1)}.git", match.group(2), match.group(3)

    pattern_root = r"^(https?://github\.com/[^/]+/[^/]+)/?$"
    match_root = re.match(pattern_root, url)
    if match_root:
        return f"{match_root.group(1)}.git", "main", "api/protos"

    error_exit(
        f"Could not parse GitHub URL: {url}. "
        "Please use format: https://github.com/user/repo/tree/branch/path"
    )
    return "", "", ""


def get_default_protos_loc(script_path: Path) -> str:
    """Determine default protos location based on repo layout.

    Args:
        script_path: Absolute path to install_xmanager.py script.

    Returns:
        Default path string or GitHub URL.
    """
    if "XMC_PROTOS_LOC" in os.environ:
        return os.environ["XMC_PROTOS_LOC"]
    local_root = (script_path.parent / ".." / "..").resolve()
    local_api_protos = local_root / "api" / "protos"
    if local_api_protos.is_dir():
        return str(local_api_protos)
    return "https://github.com/google/xmc/tree/main/api/protos"


def get_default_xmc_sdk_loc(script_path: Path) -> str:
    """Determine where to pip-install the `xmc` SDK (and `xmc login` CLI) from.

    Args:
        script_path: Absolute path to install_xmanager.py script.

    Returns:
        The local checkout's sdks/python/xmc if present, else a pip `git+https`
        requirement for github.com/google/xmc.
    """
    local_sdk = (script_path.parent / ".." / ".." / "sdks" / "python" / "xmc").resolve()
    if (local_sdk / "pyproject.toml").is_file():
        return str(local_sdk)
    return REMOTE_XMC_SDK_LOC


def parse_args(
    args: list[str] | None = None,
    script_path: Path | None = None,
) -> argparse.Namespace:
    """Parse command line arguments.

    Args:
        args: Optional raw argument list.
        script_path: Path to current script for resolving default paths.

    Returns:
        Parsed arguments Namespace.
    """
    if script_path is None:
        script_path = Path(__file__).resolve()
    default_protos = get_default_protos_loc(script_path)
    default_branch = os.environ.get("XMANAGER_BRANCH", "xm-cloud")
    default_venv = os.environ.get("VENV_DIR", "./venv")

    parser = argparse.ArgumentParser(
        description="Install XManager and setup xmanager_cloud dependencies."
    )
    parser.add_argument(
        "--protos-loc",
        default=default_protos,
        help=(
            "Location of XMC protos (local directory or GitHub URL) "
            "(default: local checkout if available, else github.com/google/xmc)"
        ),
    )
    parser.add_argument(
        "--xmanager-branch",
        default=default_branch,
        help=f"XManager git branch (default: {default_branch})",
    )
    parser.add_argument(
        "--venv",
        default=default_venv,
        help="Virtual environment target directory (default: ./venv)",
    )
    parser.add_argument(
        "--xmc-sdk-loc",
        default=get_default_xmc_sdk_loc(script_path),
        help=(
            "pip location of the xmc SDK providing the `xmc login` CLI "
            "(default: local checkout if available, else github.com/google/xmc)"
        ),
    )
    parser.add_argument(
        "--dashboard-protos-only",
        action="store_true",
        help=(
            "Only compile and install Bazel-generated Dashboard Service "
            "Python protobuf modules into the virtual environment."
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
    if not (venv_dir / "bin" / "python3").exists() and not (venv_dir / "bin" / "python").exists():
        sys.stdout.write(f"Creating virtual environment at {venv_dir}...\n")
        venv.create(venv_dir, with_pip=True)

    venv_python = venv_dir / "bin" / "python3"
    if not venv_python.exists():
        venv_python = venv_dir / "bin" / "python"
    return venv_python


def install_dependencies(venv_python: Path) -> None:
    """Upgrade pip and install dependencies.

    Args:
        venv_python: Path to virtualenv python executable.
    """
    sys.stdout.write("Updating pip and installing compile-time dependencies...\n")
    subprocess.run(
        [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
        check=True,
    )
    deps = [
        "grpcio",
        "grpcio-tools",
        "google-auth",
        "googleapis-common-protos<1.75.4",
        "requests",
        "python-dotenv",
    ]
    subprocess.run(
        [str(venv_python), "-m", "pip", "install"] + deps,
        check=True,
    )


def clone_repositories(
    xmanager_branch: str,
    xmanager_dir: Path,
    googleapis_dir: Path,
) -> None:
    """Clone XManager and Google APIs repositories.

    Args:
        xmanager_branch: Git branch for XManager repository.
        xmanager_dir: Path where XManager repository should be cloned.
        googleapis_dir: Path where Google APIs repository should be cloned.
    """
    xmanager_repo_url = "https://github.com/google-deepmind/xmanager.git"
    sys.stdout.write(f"Cloning XManager repo (branch: {xmanager_branch})...\n")
    subprocess.run(
        [
            "git",
            "clone",
            "--depth",
            "1",
            "-b",
            xmanager_branch,
            xmanager_repo_url,
            str(xmanager_dir),
        ],
        check=True,
    )
    sys.stdout.write("Cloning Google APIs repository...\n")
    subprocess.run(
        [
            "git",
            "clone",
            "--depth",
            "1",
            "https://github.com/googleapis/googleapis.git",
            str(googleapis_dir),
        ],
        check=True,
    )


def _fetch_remote_protos(protos_loc: str, xmc_protos_dir: Path) -> None:
    """Fetch protos from remote GitHub URL using sparse checkout.

    Args:
        protos_loc: GitHub tree URL for protos.
        xmc_protos_dir: Output directory for protos.
    """
    sys.stdout.write(f"Fetching remote protos from: {protos_loc}\n")
    repo_url, branch, subdir = parse_github_url(protos_loc)

    subprocess.run(
        [
            "git",
            "clone",
            "--depth",
            "1",
            "--no-checkout",
            repo_url,
            str(xmc_protos_dir),
        ],
        check=True,
    )

    subprocess.run(
        ["git", "sparse-checkout", "init", "--cone"],
        cwd=xmc_protos_dir,
        check=True,
    )
    subprocess.run(
        ["git", "sparse-checkout", "set", subdir],
        cwd=xmc_protos_dir,
        check=True,
    )
    subprocess.run(
        ["git", "checkout", branch],
        cwd=xmc_protos_dir,
        check=True,
    )

    subdir_path = xmc_protos_dir / subdir
    if subdir_path.exists():
        for item in subdir_path.iterdir():
            shutil.move(str(item), str(xmc_protos_dir / item.name))


def retrieve_protos(protos_loc: str, xmc_protos_dir: Path) -> None:
    """Retrieve protos from local path or remote repository.

    Args:
        protos_loc: Local path or GitHub URL for protos.
        xmc_protos_dir: Target directory to populate with proto files.
    """
    if protos_loc.startswith(("http://", "https://")):
        _fetch_remote_protos(protos_loc, xmc_protos_dir)
    else:
        local_path = Path(protos_loc)
        if not local_path.is_dir():
            error_exit(f"Local protos directory not found: {protos_loc}")
        sys.stdout.write(f"Using local protos from: {protos_loc}\n")
        xmc_protos_dir.mkdir(parents=True, exist_ok=True)
        api_protos = local_path / "api" / "protos"
        src = api_protos if api_protos.is_dir() else local_path
        shutil.copytree(src, xmc_protos_dir, dirs_exist_ok=True)

    exp_dir = xmc_protos_dir / "experiment"
    xid_dir = xmc_protos_dir / "xid"
    dashboard_dir = xmc_protos_dir / "dashboard"
    if not exp_dir.is_dir() or not xid_dir.is_dir() or not dashboard_dir.is_dir():
        error_exit(
            "Protos directory structure is missing required directories "
            "('experiment', 'xid', or 'dashboard')."
        )


def copy_protos_to_service(
    xmc_protos_dir: Path,
    proto_subdir: str,
    service_name: str,
    xmanager_dir: Path,
) -> None:
    """Clean and copy proto files to a specific XManager service package.

    Args:
        xmc_protos_dir: Source directory containing downloaded/local protos.
        proto_subdir: Subdirectory name within xmc_protos_dir ('xid' or
          'experiment').
        service_name: Target service name in xmanager_cloud.
        xmanager_dir: Root directory of XManager repository clone.
    """
    dest_dir = xmanager_dir / "xmanager_cloud" / service_name / "proto"
    if dest_dir.exists():
        shutil.rmtree(dest_dir)
    dest_dir.mkdir(parents=True, exist_ok=True)
    src_dir = xmc_protos_dir / proto_subdir
    shutil.copytree(src_dir, dest_dir, dirs_exist_ok=True)


def compile_protos(
    venv_python: Path,
    xmanager_dir: Path,
    googleapis_dir: Path,
) -> None:
    """Compile xmanager_cloud protos using grpc_tools.protoc.

    Args:
        venv_python: Path to virtualenv python executable.
        xmanager_dir: Root directory of XManager workspace clone.
        googleapis_dir: Root directory of Google APIs repository clone.
    """
    sys.stdout.write("Compiling xmanager_cloud protos...\n")
    xid_protos = list((xmanager_dir / "xmanager_cloud" / "xid_service" / "proto").glob("*.proto"))
    exp_protos = list(
        (xmanager_dir / "xmanager_cloud" / "experiment_state_server" / "proto").glob("*.proto")
    )
    dashboard_protos = list(
        (xmanager_dir / "xmanager_cloud" / "dashboard_service" / "proto").glob("*.proto")
    )
    proto_files = [str(p) for p in xid_protos + exp_protos + dashboard_protos]

    cmd = [
        str(venv_python),
        "-m",
        "grpc_tools.protoc",
        f"--proto_path={xmanager_dir}",
        f"--proto_path={googleapis_dir}",
        f"--python_out={xmanager_dir}",
        f"--grpc_python_out={xmanager_dir}",
    ] + proto_files
    subprocess.run(cmd, check=True)


def install_xmanager_package(venv_python: Path, xmanager_dir: Path) -> None:
    """Install XManager package into virtual environment.

    Args:
        venv_python: Path to virtualenv python executable.
        xmanager_dir: Root directory of XManager package source.
    """
    sys.stdout.write("Installing XManager library...\n")
    subprocess.run(
        [str(venv_python), "-m", "pip", "install", str(xmanager_dir)],
        check=True,
    )
    sys.stdout.write("Upgrading protobuf and grpcio dependencies to fix downgrades...\n")
    subprocess.run(
        [
            str(venv_python),
            "-m",
            "pip",
            "install",
            "--upgrade",
            "protobuf",
            "grpcio",
            "grpcio-tools",
        ],
        check=True,
    )


def install_xmc_sdk(venv_python: Path, xmc_sdk_loc: str) -> None:
    """Install the `xmc` SDK, which provides the `xmc login` CLI.

    Args:
        venv_python: Path to virtualenv python executable.
        xmc_sdk_loc: Local directory or pip `git+https` requirement.
    """
    sys.stdout.write(f"Installing xmc SDK and CLI from: {xmc_sdk_loc}\n")
    subprocess.run(
        [str(venv_python), "-m", "pip", "install", "--", xmc_sdk_loc],
        check=True,
    )


def verify_imports(venv_python: Path) -> None:
    """Verify that xmanager_cloud and generated modules import successfully.

    Args:
        venv_python: Path to virtualenv python executable.
    """
    sys.stdout.write("Verifying imports...\n")
    modules = [
        "xmanager_cloud",
        "xmanager_cloud.experiment_state_server.experiment_state_api",
        "xmanager_cloud.experiment_state_server.proto.api_pb2",
        "xmanager_cloud.dashboard_service.proto.messages_pb2",
        "xmc.auth.cli",
    ]
    for mod in modules:
        subprocess.run(
            [str(venv_python), "-c", f"import {mod}"],
            check=True,
        )


def get_venv_site_packages(venv_python: Path) -> Path:
    """Resolve the purelib site-packages directory for a virtual environment.

    Uses sysconfig.get_path('purelib') against the venv interpreter because
    site.getsitepackages() can report the base prefix instead.

    Args:
        venv_python: Path to virtualenv python executable.

    Returns:
        Resolved Path to the virtual environment site-packages directory.
    """
    res = subprocess.run(
        [
            str(venv_python),
            "-c",
            "import sysconfig; print(sysconfig.get_path('purelib'))",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return Path(res.stdout.strip())


def build_bazel_dashboard_protos(repo_root: Path) -> Path:
    """Compile Dashboard Service Python protobuf targets with Bazel.

    Queries `bazel info bazel-bin` rather than assuming `repo_root / bazel-bin`
    so custom `--symlink_prefix` or `--output_base` configurations work.

    Args:
        repo_root: Path to the XMC repository root.

    Returns:
        Path to the Bazel `_virtual_imports` directory for dashboard protos.

    Raises:
        SystemExit: If the `bazel` binary is not found in PATH.
    """
    if not shutil.which("bazel"):
        error_exit("bazel is required to build dashboard protos but was not found in PATH.")
    sys.stdout.write("Compiling Dashboard Service Python protobuf modules with Bazel...\n")
    subprocess.run(
        ["bazel", "build", *DASHBOARD_BAZEL_TARGETS],
        cwd=repo_root,
        check=True,
    )
    res = subprocess.run(
        ["bazel", "info", "bazel-bin"],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=True,
    )
    bazel_bin = Path(res.stdout.strip())
    return bazel_bin / "api" / "protos" / "dashboard" / "_virtual_imports"


def _make_tree_user_writable(target_dir: Path) -> None:
    """Grant user write permission recursively across a directory tree.

    Bazel leaves build outputs read-only; adding user write permissions
    after each copy allows subsequent copies or re-runs to overwrite files.

    Args:
        target_dir: Root directory to make user-writable.
    """
    if not target_dir.exists():
        return
    target_dir.chmod(target_dir.stat().st_mode | stat.S_IWUSR)
    for path in target_dir.rglob("*"):
        path.chmod(path.stat().st_mode | stat.S_IWUSR)


def copy_virtual_imports_to_site_packages(
    virtual_imports_dir: Path,
    site_packages_dir: Path,
) -> None:
    """Copy Bazel `_virtual_imports` dashboard protos into venv site-packages.

    Clears only `xmanager_cloud/dashboard_service/proto` before copying so
    re-runs after a proto change cannot leave orphaned modules behind while
    preserving sibling `xmanager_cloud` packages installed from XManager.

    Args:
        virtual_imports_dir: Path to Bazel `dashboard/_virtual_imports`.
        site_packages_dir: Path to the target venv `site-packages` directory.

    Raises:
        SystemExit: If expected `_virtual_imports` proto directories are missing.
    """
    sys.stdout.write(
        "Copying generated protobuf modules to virtual environment "
        f"site-packages: {site_packages_dir}...\n"
    )
    dest_pkg = site_packages_dir / "xmanager_cloud"
    dest_service_dir = dest_pkg / "dashboard_service"
    dashboard_proto_dir = dest_service_dir / "proto"
    if dest_service_dir.exists():
        dest_pkg.chmod(dest_pkg.stat().st_mode | stat.S_IWUSR)
        dest_service_dir.chmod(dest_service_dir.stat().st_mode | stat.S_IWUSR)
    if dashboard_proto_dir.exists():
        _make_tree_user_writable(dashboard_proto_dir)
        shutil.rmtree(dashboard_proto_dir)
    dest_pkg.mkdir(parents=True, exist_ok=True)
    dest_pkg.chmod(dest_pkg.stat().st_mode | stat.S_IWUSR)

    for proto_target in DASHBOARD_VIRTUAL_IMPORT_TARGETS:
        src_service_dir = (
            virtual_imports_dir / proto_target / "xmanager_cloud" / "dashboard_service"
        )
        if not src_service_dir.is_dir():
            error_exit(f"Bazel virtual imports directory not found: {src_service_dir}")
        shutil.copytree(src_service_dir, dest_service_dir, dirs_exist_ok=True)
        _make_tree_user_writable(dest_service_dir)


def install_bazel_dashboard_protos(venv_python: Path, repo_root: Path) -> None:
    """Build and install Bazel Dashboard Service Python protos into a venv.

    Args:
        venv_python: Path to virtualenv python executable.
        repo_root: Path to the XMC repository root.
    """
    virtual_imports_dir = build_bazel_dashboard_protos(repo_root)
    site_packages_dir = get_venv_site_packages(venv_python)
    copy_virtual_imports_to_site_packages(virtual_imports_dir, site_packages_dir)


def _run_full_install(args: argparse.Namespace, venv_python: Path) -> None:
    """Clone XManager, compile all service protos, and install the package.

    Args:
        args: Parsed CLI arguments.
        venv_python: Path to virtualenv python executable.
    """
    install_dependencies(venv_python)
    with tempfile.TemporaryDirectory() as temp_dir_str:
        temp_dir = Path(temp_dir_str)
        xmanager_dir = temp_dir / "xmanager"
        googleapis_dir = temp_dir / "googleapis"
        xmc_protos_dir = temp_dir / "xmc_protos"

        clone_repositories(args.xmanager_branch, xmanager_dir, googleapis_dir)
        retrieve_protos(args.protos_loc, xmc_protos_dir)

        sys.stdout.write("Copying protos into xmanager_cloud...\n")
        copy_protos_to_service(xmc_protos_dir, "xid", "xid_service", xmanager_dir)
        copy_protos_to_service(
            xmc_protos_dir, "experiment", "experiment_state_server", xmanager_dir
        )
        copy_protos_to_service(xmc_protos_dir, "dashboard", "dashboard_service", xmanager_dir)

        compile_protos(venv_python, xmanager_dir, googleapis_dir)
        install_xmanager_package(venv_python, xmanager_dir)
        install_xmc_sdk(venv_python, args.xmc_sdk_loc)
        verify_imports(venv_python)


def main(raw_args: list[str] | None = None) -> None:
    """Main execution function for install_xmanager script.

    Args:
        raw_args: Optional CLI arguments list for parameter overrides.
    """
    args = parse_args(raw_args)
    venv_dir = Path(args.venv).resolve()

    if args.dashboard_protos_only:
        repo_root = (Path(__file__).resolve().parent / ".." / "..").resolve()
        venv_python = setup_virtualenv(venv_dir)
        install_bazel_dashboard_protos(venv_python, repo_root)
        return

    check_prerequisites()
    check_git_version()

    sys.stdout.write("=== Configuration ===\n")
    sys.stdout.write(f"Protos Location: {args.protos_loc}\n")
    sys.stdout.write(f"xmc SDK:         {args.xmc_sdk_loc}\n")
    sys.stdout.write(
        "XManager Repo:   https://github.com/google-deepmind/xmanager.git "
        f"(branch: {args.xmanager_branch})\n"
    )
    sys.stdout.write(f"Venv Target:     {venv_dir}\n")
    sys.stdout.write("=====================\n")

    venv_python = setup_virtualenv(venv_dir)
    _run_full_install(args, venv_python)

    sys.stdout.write("==================================================\n")
    sys.stdout.write("Installation complete!\n")
    sys.stdout.write("To activate the virtual environment, run:\n")
    sys.stdout.write(f"  source {venv_dir}/bin/activate\n")
    sys.stdout.write("Then sign in to your XMC deployment as yourself:\n")
    sys.stdout.write("  xmc login --server https://<your-xmc-ui-host>\n")
    sys.stdout.write("==================================================\n")


if __name__ == "__main__":
    main()
