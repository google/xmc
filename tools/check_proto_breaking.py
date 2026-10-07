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
ALLOW_BREAKING_NOTE = "These breaking changes do not fail the check: it ran with --allow-breaking."
Finding = dict[str, Any]  # One JSON object of the `buf breaking --error-format=json` output.


def run_captured(cmd: list[str], cwd: Path, check: bool = True) -> subprocess.CompletedProcess[str]:
    """Runs cmd in cwd with captured output, keeping stdout reserved for the JSON result."""
    return run_command(
        cmd, check=check, capture_output=True, log_on_error=False, log_cmd=False, cwd=cwd
    )


def print_warning(message: str) -> None:
    """Prints a non-fatal warning to stderr, keeping stdout reserved for the JSON result."""
    print(f"WARNING: {message}", file=sys.stderr)


def run_git(repo_root: Path, *git_args: str, strip: bool = True) -> str:
    """Runs a git command in repo_root and returns its stdout, stripped unless strip is False.

    Raises:
        HandledError: If the git command fails.
    """
    try:
        stdout = run_captured(["git", *git_args], cwd=repo_root).stdout
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"'git {' '.join(git_args)}' failed: {e.stderr.strip()}",
            f"Check the base ref (run 'git fetch origin' to refresh {DEFAULT_UPSTREAM_REF}) "
            "and remove stale temporary worktrees with 'git worktree prune'.",
        ) from e
    return stdout.strip() if strip else stdout


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


def resolve_buf_config(repo_root: Path, base_commit: str) -> tuple[str, str]:
    """Reads the buf rules of the base revision so that a change cannot relax its own check.

    Returns:
        (config, source): the value for `buf --config` and a description of its origin. The
        base's BUF_CONFIG_FILE content is passed inline; only if the base revision lacks that
        file is the working tree's file used instead, with a warning.

    Raises:
        HandledError: If git fails to list or read the file in the base revision.
    """
    source = f"{base_commit}:{BUF_CONFIG_FILE}"
    # Empty output means that the file is missing; `git cat-file -e` cannot tell that apart
    # from other errors.
    if not run_git(repo_root, "ls-tree", "--name-only", base_commit, "--", BUF_CONFIG_FILE):
        print_warning(f"{source} does not exist; using the working tree's {BUF_CONFIG_FILE}.")
        return str(repo_root / BUF_CONFIG_FILE), f"{BUF_CONFIG_FILE} (working tree)"
    print(f"Using the buf rules of the base revision ({source}).", file=sys.stderr)
    return run_git(repo_root, "show", source, strip=False), source


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


def run_buf_breaking(buf: str, head_image: Path, base_image: Path, config: str) -> list[Finding]:
    """Runs `buf breaking` and returns its findings; an empty list means compatible.

    Raises:
        HandledError: If buf fails or signals breaking changes without any finding.
    """
    images = [str(head_image), "--against", str(base_image)]
    cmd = [buf, "breaking", *images, "--config", config, "--error-format=json"]
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
    repo_root: Path, base_commit: str, buf: str, config: str, bazel_flags: list[str]
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
        return run_buf_breaking(buf, head_image, base_image, config)


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
        "and names of deleted fields. If the break is intentional, see 'Protobuf Breaking "
        "Changes' in docs/developer.md for the pull request override.",
    )


def append_markdown_report(report_file: Path, message: str, note: str) -> None:
    """Appends breaking changes and a note as Markdown to report_file, e.g. the job summary.

    A write failure is only a warning so that it never masks the breaking changes.
    """
    report = f"### Breaking protobuf API changes\n\n```text\n{message}\n```\n\n{note}\n"
    try:
        with report_file.open("a", encoding="utf-8") as stream:
            stream.write(report)
    except OSError as e:
        print_warning(f"Could not write the report to {report_file}: {e}")


def report_breaking_changes(
    args: argparse.Namespace, base_commit: str, findings: list[Finding]
) -> list[str]:
    """Appends findings to args.report_file when it is set and returns them formatted.

    Raises:
        HandledError: Listing the findings, unless args.allow_breaking is set.
    """
    error = breaking_changes_error(base_commit, findings)
    if args.report_file:
        note = ALLOW_BREAKING_NOTE if args.allow_breaking else error.instruction
        append_markdown_report(args.report_file, error.message, note)
    if not args.allow_breaking:
        raise error
    return [format_finding(finding) for finding in findings]


def check_proto_breaking(args: argparse.Namespace) -> dict[str, Any]:
    """Compares the working tree's protobuf APIs against the base revision.

    Breaking changes are also appended to args.report_file as Markdown when it is set. With
    args.allow_breaking, they are reported without failing; other errors still fail.

    Returns:
        {"status": "passed", "base_commit": <base SHA>, "config": <origin of the buf rules>}.
        Allowed breaking changes turn "status" into "overridden" and are listed under
        "breaking_changes".

    Raises:
        HandledError: If buf reports breaking changes without args.allow_breaking, or a
            build, the coverage guard, or another prerequisite fails.
    """
    buf = resolve_buf_binary()
    base_commit = resolve_base_commit(_REPO_ROOT, args.base_ref)
    config, config_source = resolve_buf_config(_REPO_ROOT, base_commit)
    findings = compare_against_base(_REPO_ROOT, base_commit, buf, config, args.bazel_flags)
    passed = {"status": "passed", "base_commit": base_commit, "config": config_source}
    if not findings:
        return passed
    allowed = report_breaking_changes(args, base_commit, findings)
    return passed | {"status": "overridden", "breaking_changes": allowed}


def build_parser() -> argparse.ArgumentParser:
    """Builds the command-line parser of the tool."""
    parser = argparse.ArgumentParser(
        description=f"Compare {DESCRIPTOR_SET_TARGET} of the working tree against a base "
        "revision built in a temporary git worktree with 'buf breaking', using the base "
        f"revision's {BUF_CONFIG_FILE}."
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
    parser.add_argument(
        "--report-file",
        type=Path,
        help="Markdown file to append breaking changes to, e.g. $GITHUB_STEP_SUMMARY.",
    )
    parser.add_argument(
        "--allow-breaking",
        action="store_true",
        help="Report breaking changes without failing, e.g. for an approved override; build "
        "and coverage errors still fail.",
    )
    return parser


def main() -> None:
    """Main entrypoint for the check_proto_breaking tool."""
    run_tool(build_parser(), check_proto_breaking)


if __name__ == "__main__":
    main()
