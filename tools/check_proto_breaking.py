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

"""Tool for detecting breaking changes in the first-party protobuf APIs with buf."""

import argparse
import collections
import contextlib
import json
import shlex
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Iterator
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_command, run_tool  # noqa: E402

DESCRIPTOR_SET_TARGET = "//api:all_protos_descriptor_set"
UNCOVERED_PROTOS_QUERY = (
    'labels(srcs, kind("^proto_library rule$", //api/...)) except '
    f'labels(srcs, kind("^proto_library rule$", deps({DESCRIPTOR_SET_TARGET})))'
)
BUF_CONFIG_FILE = "api/buf-breaking.yaml"
DEFAULT_UPSTREAM_REF = "origin/main"
BUF_INSTALL_URL = "https://buf.build/docs/cli/installation/"
BUF_FINDINGS_EXIT_CODE = 100
BAZEL_LOG_TAIL_LINES = 40
Finding = dict[str, Any]  # One JSON object of the `buf breaking --error-format=json` output.


def run_captured(cmd: list[str], cwd: Path, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Runs cmd in cwd with captured output, keeping stdout reserved for the JSON result."""
    return run_command(
        cmd, check=check, capture_output=True, log_on_error=False, log_cmd=False, cwd=cwd
    )


def print_warning(message: str) -> None:
    """Prints a non-fatal warning to stderr, keeping stdout reserved for the JSON result."""
    print(f"WARNING: {message}", file=sys.stderr)


def run_git(repo_root: Path, *git_args: str) -> str:
    """Runs a git command in repo_root and returns its stripped stdout.

    Raises:
        HandledError: If the git command fails.
    """
    try:
        return run_captured(["git", *git_args], cwd=repo_root).stdout.strip()
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"'git {' '.join(git_args)}' failed: {e.stderr.strip()}",
            f"Check the base ref (run 'git fetch origin' to refresh {DEFAULT_UPSTREAM_REF}) "
            "and remove stale temporary worktrees with 'git worktree prune'.",
        ) from e


def bazel_error(workspace: Path, bazel_args: list[str], log_tail: list[str]) -> HandledError:
    """Builds the error reported for a failed Bazel command from the tail of its log."""
    return HandledError(
        f"'bazel {bazel_args[0]}' failed in {workspace}:\n" + "\n".join(log_tail),
        f"Fix the Bazel error above; reproduce it with: {shlex.join(['bazel', *bazel_args])}",
    )


def run_bazel_build(workspace: Path, bazel_flags: list[str]) -> None:
    """Builds DESCRIPTOR_SET_TARGET in workspace, streaming Bazel's log to stderr.

    Raises:
        HandledError: If the build fails; the message carries the last log lines.
    """
    log_tail: collections.deque[str] = collections.deque(maxlen=BAZEL_LOG_TAIL_LINES)
    bazel_args = ["build", *bazel_flags, DESCRIPTOR_SET_TARGET]
    with subprocess.Popen(
        ["bazel", *bazel_args],
        cwd=workspace,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    ) as process:
        for line in process.stdout or ():
            sys.stderr.write(line)
            log_tail.append(line.rstrip("\n"))
    if process.returncode != 0:
        raise bazel_error(workspace, bazel_args, list(log_tail))


def capture_bazel_stdout(workspace: Path, *bazel_args: str) -> str:
    """Runs a Bazel query or info command in workspace and returns its stdout.

    Raises:
        HandledError: If the command fails; the message carries the last stderr lines.
    """
    try:
        return run_captured(["bazel", *bazel_args], cwd=workspace).stdout
    except subprocess.CalledProcessError as e:
        log_tail = e.stderr.splitlines()[-BAZEL_LOG_TAIL_LINES:]
        raise bazel_error(workspace, list(bazel_args), log_tail) from e


def resolve_buf_binary() -> str:
    """Returns the path of the buf executable found on PATH.

    Raises:
        HandledError: If buf is not on PATH.
    """
    resolved = shutil.which("buf")
    if not resolved:
        raise HandledError("buf is not on PATH.", f"Install buf (see {BUF_INSTALL_URL}).")
    return resolved


def resolve_base_commit(repo_root: Path, base_ref: str | None) -> str:
    """Resolves base_ref, or else the merge-base of HEAD and origin/main, to a commit SHA."""
    if base_ref:
        return run_git(repo_root, "rev-parse", "--verify", f"{base_ref}^{{commit}}")
    return run_git(repo_root, "merge-base", "HEAD", DEFAULT_UPSTREAM_REF)


def build_descriptor_set(workspace: Path, bazel_flags: list[str], destination: Path) -> None:
    """Builds the aggregate descriptor set in workspace and copies it to destination.

    Raises:
        HandledError: If the build fails or does not yield exactly one output file.
    """
    run_bazel_build(workspace, bazel_flags)
    cquery = ["cquery", *bazel_flags, "--output=files", DESCRIPTOR_SET_TARGET]
    outputs = capture_bazel_stdout(workspace, *cquery).splitlines()
    if len(outputs) != 1:
        raise HandledError(
            f"Expected exactly one output file for {DESCRIPTOR_SET_TARGET}, got {outputs}.",
            f"Ensure {DESCRIPTOR_SET_TARGET} is a proto_descriptor_set target.",
        )
    # The execution root is the same for every build configuration, so no flags are passed.
    info = capture_bazel_stdout(workspace, "info", "execution_root")
    shutil.copyfile(Path(info.strip()) / outputs[0], destination)


def ensure_full_coverage(repo_root: Path) -> None:
    """Verifies that DESCRIPTOR_SET_TARGET covers every proto_library under //api.

    Raises:
        HandledError: Listing the proto files that the comparison would silently skip.
    """
    uncovered = capture_bazel_stdout(repo_root, "query", UNCOVERED_PROTOS_QUERY).split()
    if not uncovered:
        return
    raise HandledError(
        f"{DESCRIPTOR_SET_TARGET} misses {len(uncovered)} proto file(s) of //api "
        "proto_library targets:\n" + "\n".join(uncovered),
        f"Add the proto_library targets owning these files to {DESCRIPTOR_SET_TARGET}.",
    )


def run_cleanup(cmd: list[str], cwd: Path, hint: str = "") -> None:
    """Runs a cleanup command; failures print a warning plus hint so they never mask errors."""
    try:
        result = run_captured(cmd, cwd=cwd, check=False)
        if result.returncode == 0:
            return
        error = result.stderr.strip() or f"exit code {result.returncode}"
    except OSError as e:  # For example, the executable is missing.
        error = str(e)
    print_warning(f"'{shlex.join(cmd)}' failed in {cwd} ({error}). {hint}".rstrip())


def expunge_output_base(worktree: Path) -> None:
    """Deletes the Bazel output base of worktree, which also shuts down its server."""
    run_cleanup(["bazel", "clean", "--expunge"], worktree)


def remove_worktree(repo_root: Path, worktree: Path) -> None:
    """Removes the temporary worktree, warning instead of raising on failure."""
    # --force only ever discards the throwaway worktree that this tool created.
    cmd = ["git", "worktree", "remove", "--force", str(worktree)]
    run_cleanup(cmd, repo_root, "Run 'git worktree prune' to clean it up.")


@contextlib.contextmanager
def base_worktree(repo_root: Path, base_commit: str, scratch_dir: Path) -> Iterator[Path]:
    """Checks out base_commit in a detached worktree; cleanup never masks other errors."""
    worktree = scratch_dir / "base"
    run_git(repo_root, "worktree", "add", "--detach", str(worktree), base_commit)
    try:
        yield worktree
    finally:
        try:
            expunge_output_base(worktree)
        finally:
            remove_worktree(repo_root, worktree)


def run_buf_breaking(buf: str, head_image: Path, base_image: Path, config: Path) -> list[Finding]:
    """Runs `buf breaking` and returns its findings; an empty list means compatible.

    Raises:
        HandledError: If buf fails or signals breaking changes without any finding.
    """
    images = [str(head_image), "--against", str(base_image)]
    cmd = [buf, "breaking", *images, "--config", str(config), "--error-format=json"]
    result = run_captured(cmd, cwd=head_image.parent, check=False)
    if result.returncode == 0:
        return []
    if result.returncode == BUF_FINDINGS_EXIT_CODE:
        findings: list[Finding] = []
        for line in filter(str.strip, result.stdout.splitlines()):
            try:
                findings.append(json.loads(line))
            except json.JSONDecodeError:
                print_warning(f"Skipping non-JSON buf output: {line}")
        if findings:
            return findings
    raise HandledError(
        f"buf breaking failed with exit code {result.returncode} without reporting "
        f"findings: {result.stderr.strip()}",
        f"Verify {BUF_CONFIG_FILE} and the installed buf version.",
    )


def compare_against_base(
    repo_root: Path, base_commit: str, buf: str, bazel_flags: list[str]
) -> list[Finding]:
    """Builds the working tree and base descriptor sets and returns buf's findings.

    Raises:
        HandledError: If coverage is incomplete, a build fails, or buf fails.
    """
    with tempfile.TemporaryDirectory(prefix="proto-breaking-") as scratch:
        scratch_dir = Path(scratch)
        head_image = scratch_dir / "head.binpb"
        base_image = scratch_dir / "base.binpb"
        build_descriptor_set(repo_root, bazel_flags, head_image)
        ensure_full_coverage(repo_root)
        with base_worktree(repo_root, base_commit, scratch_dir) as worktree:
            build_descriptor_set(worktree, bazel_flags, base_image)
        return run_buf_breaking(buf, head_image, base_image, repo_root / BUF_CONFIG_FILE)


def format_finding(finding: Finding) -> str:
    """Formats a buf JSON finding as '[RULE] path: message'.

    Line numbers are omitted because Bazel descriptor sets carry no source info.
    """
    location = f"{finding['path']}: " if finding.get("path") else ""
    return f"[{finding.get('type', 'UNKNOWN')}] {location}{finding.get('message', '')}"


def breaking_changes_error(base_commit: str, findings: list[Finding]) -> HandledError:
    """Builds the error that lists the breaking changes found against base_commit."""
    details = "\n".join(format_finding(finding) for finding in findings)
    return HandledError(
        f"Found {len(findings)} breaking protobuf change(s) against base {base_commit}:\n{details}",
        "Keep the API backward compatible: add new fields, messages, or RPCs instead of "
        "removing, renaming, renumbering, or retyping existing ones, and reserve the numbers "
        "and names of deleted fields.",
    )


def check_proto_breaking(args: argparse.Namespace) -> dict[str, str]:
    """Compares the working tree's protobuf APIs against the base revision.

    Returns:
        {"status": "passed", "base_commit": <base SHA>, "config": BUF_CONFIG_FILE}.

    Raises:
        HandledError: If buf reports breaking changes or a prerequisite is missing.
    """
    buf = resolve_buf_binary()
    base_commit = resolve_base_commit(_REPO_ROOT, args.base_ref)
    findings = compare_against_base(_REPO_ROOT, base_commit, buf, args.bazel_flags)
    if not findings:
        return {"status": "passed", "base_commit": base_commit, "config": BUF_CONFIG_FILE}
    raise breaking_changes_error(base_commit, findings)


def build_parser() -> argparse.ArgumentParser:
    """Builds the command-line parser of the tool."""
    parser = argparse.ArgumentParser(
        description=f"Compare {DESCRIPTOR_SET_TARGET} of the working tree against a base "
        f"revision built in a temporary git worktree with 'buf breaking' ({BUF_CONFIG_FILE})."
    )
    parser.add_argument(
        "--base-ref",
        help=f"Git revision to compare against (default: merge-base with {DEFAULT_UPSTREAM_REF}).",
    )
    parser.add_argument(
        "--bazel-flag",
        action="append",
        default=[],
        dest="bazel_flags",
        metavar="FLAG",
        help="Extra Bazel flag for both builds (repeatable), e.g. --bazel-flag=--config=ci.",
    )
    return parser


def main() -> None:
    """Main entrypoint for the check_proto_breaking tool."""
    run_tool(build_parser(), check_proto_breaking)


if __name__ == "__main__":
    main()
