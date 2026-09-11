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
import subprocess
import sys
import tempfile
import venv
from pathlib import Path

MIN_GIT_VERSION = (2, 25)
DEFAULT_XMANAGER_UI_URL = "https://dev.xmanageriso.dev"


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

    default_ui_url = os.environ.get("DEFAULT_XMANAGER_UI_URL") or os.environ.get(
        "XMANAGER_UI_URL", DEFAULT_XMANAGER_UI_URL
    )

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
        "--default-ui-url",
        default=default_ui_url,
        help=f"Default XManager UI URL to patch into xm_cloud (default: {default_ui_url})",
    )
    return parser.parse_args(args)


def setup_virtualenv(venv_dir: Path) -> Path:
    """Create virtual environment if it does not exist.

    Args:
        venv_dir: Path to virtual environment directory.

    Returns:
        Path to python executable inside virtual environment.
    """
    if not venv_dir.exists():
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
        "googleapis-common-protos",
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


def rewrite_proto_imports(proto_dir: Path) -> None:
    """Rewrite proto import paths in-place for all .proto files in proto_dir.

    Replaces 'third_party/xmanager_cloud/' with 'xmanager_cloud/'.

    Args:
        proto_dir: Path to directory containing .proto files.
    """
    for proto_file in proto_dir.glob("*.proto"):
        content = proto_file.read_text(encoding="utf-8")
        updated = content.replace("third_party/xmanager_cloud/", "xmanager_cloud/")
        if updated != content:
            proto_file.write_text(updated, encoding="utf-8")


def patch_default_ui_url(
    xmanager_dir: Path,
    default_ui_url: str = DEFAULT_XMANAGER_UI_URL,
) -> None:
    """Patch the default XManager UI URL in xm_cloud to prevent printing alpha env.

    Args:
        xmanager_dir: Root directory of cloned XManager repository.
        default_ui_url: Base URL to replace the default alpha URL with.
    """
    experiment_file = xmanager_dir / "xmanager" / "xm_cloud" / "experiment.py"
    if not experiment_file.is_file():
        sys.stderr.write(f"Warning: {experiment_file} not found; skipping default UI URL patch.\n")
        return

    content = experiment_file.read_text(encoding="utf-8")
    updated = re.sub(
        r"_DEFAULT_XMANAGER_UI_URL\s*=\s*['\"][^'\"]+['\"]",
        f"_DEFAULT_XMANAGER_UI_URL = '{default_ui_url}'",
        content,
    )
    if updated != content:
        experiment_file.write_text(updated, encoding="utf-8")
        sys.stdout.write(f"Patched default XManager UI URL to: {default_ui_url}\n")
    else:
        sys.stderr.write(
            "Warning: _DEFAULT_XMANAGER_UI_URL pattern not found in experiment.py; patch not applied.\n"
        )


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
    ]
    for mod in modules:
        subprocess.run(
            [str(venv_python), "-c", f"import {mod}"],
            check=True,
        )


def main(raw_args: list[str] | None = None) -> None:
    """Main execution function for install_xmanager script.

    Args:
        raw_args: Optional CLI arguments list for parameter overrides.
    """
    check_prerequisites()
    check_git_version()

    args = parse_args(raw_args)
    venv_dir = Path(args.venv).resolve()

    sys.stdout.write("=== Configuration ===\n")
    sys.stdout.write(f"Protos Location: {args.protos_loc}\n")
    sys.stdout.write(
        "XManager Repo:   https://github.com/google-deepmind/xmanager.git "
        f"(branch: {args.xmanager_branch})\n"
    )
    sys.stdout.write(f"Venv Target:     {venv_dir}\n")
    sys.stdout.write("=====================\n")

    venv_python = setup_virtualenv(venv_dir)
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

        sys.stdout.write("Adjusting proto import paths...\n")
        rewrite_proto_imports(xmanager_dir / "xmanager_cloud" / "xid_service" / "proto")
        rewrite_proto_imports(xmanager_dir / "xmanager_cloud" / "experiment_state_server" / "proto")
        rewrite_proto_imports(xmanager_dir / "xmanager_cloud" / "dashboard_service" / "proto")

        sys.stdout.write("Patching default XManager UI URL...\n")
        patch_default_ui_url(xmanager_dir, args.default_ui_url)

        compile_protos(venv_python, xmanager_dir, googleapis_dir)

        install_xmanager_package(venv_python, xmanager_dir)
        verify_imports(venv_python)

    sys.stdout.write("==================================================\n")
    sys.stdout.write("Installation complete!\n")
    sys.stdout.write("To activate the virtual environment, run:\n")
    sys.stdout.write(f"  source {venv_dir}/bin/activate\n")
    sys.stdout.write("==================================================\n")


if __name__ == "__main__":
    main()
