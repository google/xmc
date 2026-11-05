#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""One-shot CLI tool to discover and triage broken Nightly Examples E2E runs on main."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import stat
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests.workflow_matrix import parse_matrix_items, resolve_workflow_path

_REPO = "google/xmc"
_BRANCH = "main"
_E2E_RUN_LIMIT = 10
_BUILD_RUN_LIMIT = 15
_RECENT_COMMIT_LIMIT = 20
_RANGE_COMMIT_LIMIT = 30
_ERROR_CONTEXT_LINES = 5
_MAX_EXCERPT_LINES = 15
_MAX_EXCERPT_CHARS = 300
_MAX_RESULT_BYTES = 65536

_SHA_RE = re.compile(r"^[0-9a-fA-F]{7,40}$")
_ARTIFACT_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
_LOG_PREFIX_RE = re.compile(r"^\ufeff?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\s+")
_ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")

_FAILURE_CONCLUSIONS = frozenset({"failure", "timed_out", "startup_failure", "action_required"})
_HEALTHY_CONCLUSIONS = frozenset({"success", "skipped", "neutral"})
_CLUSTER_DEPLOYED_PREFIXES = (
    "cmd/",
    "internal/",
    "pkg/",
    "api/",
    "deploy/helm/",
    "deploy/ctk_v2/",
    "deploy/addons/",
    "deploy/clickhouse/",
    "deploy/profiles/",
)

_E2E_RUN_FIELDS = "databaseId,status,conclusion,headSha,event,createdAt,url"
_BUILD_RUN_FIELDS = "databaseId,status,conclusion,headSha,createdAt,url"
_ISSUE_FIELDS = "number,title,url,createdAt"
_CLI_HINT = "Ensure 'gh' and 'git' CLIs are on PATH and run 'gh auth login'."


@dataclass
class _TriageContext:
    """Shared context for subprocess execution, scratch storage, and non-fatal warnings."""

    repo_dir: Path | None
    warnings: list[str] = field(default_factory=list)
    scratch_dir: Path = Path("/tmp")
    e2e_runs: list[dict[str, Any]] = field(default_factory=list)


@dataclass(frozen=True)
class _RunSelection:
    """Resolved target run and optional superseding scheduled green run."""

    target_run: dict[str, Any] | None
    superseded_by: dict[str, Any] | None


def _run_quiet(cmd: list[str], cwd: Path | None) -> subprocess.CompletedProcess[str]:
    """Runs a subprocess quietly, converting missing CLI binaries into HandledError."""
    return run_command(
        cmd,
        cwd=cwd,
        capture_output=True,
        check=False,
        log_cmd=False,
        errors="replace",
        handled_instruction=_CLI_HINT,
    )


def _gh_json(ctx: _TriageContext, gh_args: list[str], *, fatal: bool = True) -> Any:
    """Executes a `gh` subcommand and parses JSON output, raising or warning on failure."""
    res = _run_quiet(["gh", *gh_args], ctx.repo_dir)
    label = f"gh {' '.join(gh_args[:3])}"
    error = ""
    if res.returncode != 0:
        error = f"'{label}' failed: {res.stderr.strip() or f'exit {res.returncode}'}"
    if not error:
        try:
            return json.loads(res.stdout)
        except json.JSONDecodeError as exc:
            error = f"Invalid JSON from '{label}': {exc}"

    if fatal:
        raise HandledError(
            error, "Verify GitHub CLI authentication ('gh auth status') and network."
        )
    ctx.warnings.append(error)
    return None


def _list_workflow_runs(
    ctx: _TriageContext, workflow: str, limit: int, fields: str, *, fatal: bool = True
) -> list[dict[str, Any]]:
    """Lists recent GitHub Actions runs for `workflow` on `main`."""
    scope_flags = [f"--repo={_REPO}", f"--branch={_BRANCH}", f"--workflow={workflow}"]
    gh_args = ["run", "list", *scope_flags, f"--limit={limit}", f"--json={fields}"]
    return _gh_json(ctx, gh_args, fatal=fatal) or []


def _load_matrix_name_map(repo_dir: Path | None) -> dict[str, str]:
    """Maps `'Run & Verify: <matrix.example.name>'` job titles back to `matrix.example.id`."""
    workflow_path = resolve_workflow_path(repo_dir)
    if not workflow_path.is_file():
        return {}
    items = parse_matrix_items(workflow_path.read_text(encoding="utf-8"))
    return {
        f"Run & Verify: {item['name']}": item["id"]
        for item in items
        if item.get("name") and item.get("id")
    }


def _is_newer_or_equal(run: dict[str, Any] | None, reference: dict[str, Any] | None) -> bool:
    """Returns True when `run.createdAt >= reference.createdAt`."""
    if not run or not reference:
        return False
    run_created, ref_created = run.get("createdAt"), reference.get("createdAt")
    return bool(run_created and ref_created and str(run_created) >= str(ref_created))


def _is_failed_run(run: dict[str, Any] | None) -> bool:
    """Returns True when `run` has completed with a non-healthy conclusion."""
    return bool(
        run
        and run.get("status") == "completed"
        and run.get("conclusion") not in _HEALTHY_CONCLUSIONS
    )


def _classify_status(
    target_run: dict[str, Any] | None, has_completed: bool, superseded_by: dict[str, Any] | None
) -> str:
    """Classifies overall nightly triage state from the resolved target run."""
    if target_run is None:
        return "healthy" if has_completed else "no_completed_runs"
    if target_run.get("status") != "completed":
        return "in_progress"
    if not _is_failed_run(target_run):
        return "healthy"
    return "superseded" if superseded_by is not None else "failing"


def _warn_if_manual_green_newer(
    ctx: _TriageContext,
    target_run: dict[str, Any] | None,
    superseded_by: dict[str, Any] | None,
    manual_green: dict[str, Any] | None,
) -> None:
    """Appends a warning when a newer non-scheduled green run does not supersede a failure."""
    if (
        _is_failed_run(target_run)
        and superseded_by is None
        and _is_newer_or_equal(manual_green, target_run)
        and manual_green is not None
    ):
        event_name = manual_green.get("event") or "manual"
        ctx.warnings.append(
            f"Newer green {event_name} run {manual_green.get('databaseId')} "
            "(possibly filtered) not treated as superseding."
        )


def _resolve_target_run(
    pinned_run: dict[str, Any] | None,
    scheduled_green: dict[str, Any] | None,
    latest_failure: dict[str, Any] | None,
) -> dict[str, Any] | None:
    """Resolves the target run to triage after applying scheduled-only supersession."""
    if pinned_run is not None:
        return pinned_run
    if _is_newer_or_equal(scheduled_green, latest_failure):
        return None
    return latest_failure


def _select_target_run(
    ctx: _TriageContext,
    completed: list[dict[str, Any]],
    pinned_run: dict[str, Any] | None,
) -> _RunSelection:
    """Selects the target E2E run and records non-superseding manual green warnings."""
    scheduled_green = next(
        (r for r in completed if r.get("conclusion") == "success" and r.get("event") == "schedule"),
        None,
    )
    manual_green = next(
        (r for r in completed if r.get("conclusion") == "success" and r.get("event") != "schedule"),
        None,
    )
    latest_failure = next(
        (r for r in completed if r.get("conclusion") not in _HEALTHY_CONCLUSIONS), None
    )

    target_run = _resolve_target_run(pinned_run, scheduled_green, latest_failure)
    is_superseded = _is_failed_run(target_run) and _is_newer_or_equal(scheduled_green, target_run)
    superseded_by = scheduled_green if is_superseded else None
    _warn_if_manual_green_newer(ctx, target_run, superseded_by, manual_green)
    return _RunSelection(target_run=target_run, superseded_by=superseded_by)


def _evaluate_runs(
    ctx: _TriageContext,
    e2e_runs: list[dict[str, Any]],
    pinned_run: dict[str, Any] | None,
    open_issues: list[dict[str, Any]],
) -> dict[str, Any]:
    """Evaluates E2E runs for scheduled-only supersession and builds the base triage report."""
    completed = [run for run in e2e_runs if run.get("status") == "completed"]
    if pinned_run is None and not completed:
        ctx.warnings.append("No completed nightly-examples-e2e.yml runs found.")

    selection = _select_target_run(ctx, completed, pinned_run)
    status_summary = _classify_status(
        selection.target_run, bool(completed), selection.superseded_by
    )
    return {
        "status_summary": status_summary,
        "target_run": selection.target_run,
        "superseded_by_green_run": selection.superseded_by,
        "open_issues": open_issues,
        "failed_jobs": [],
        "downloaded_artifacts": [],
        "recent_commits": [],
        "rollout_skew": None,
        "scratch_dir": None,
        "warnings": ctx.warnings,
    }


def _discover_nightly_status(ctx: _TriageContext, run_id: int | None) -> dict[str, Any]:
    """Fetches `origin/main`, queries recent E2E runs and open issues, and evaluates run status."""
    fetch_res = _run_quiet(["git", "fetch", "--", "origin", _BRANCH], ctx.repo_dir)
    if fetch_res.returncode != 0:
        ctx.warnings.append(f"git fetch origin {_BRANCH} failed: {fetch_res.stderr.strip()}")

    ctx.e2e_runs = _list_workflow_runs(
        ctx, "nightly-examples-e2e.yml", _E2E_RUN_LIMIT, _E2E_RUN_FIELDS
    )
    issue_flags = [f"--repo={_REPO}", "--label=nightly-failure", "--state=open"]
    open_issues = (
        _gh_json(ctx, ["issue", "list", *issue_flags, f"--json={_ISSUE_FIELDS}"], fatal=False) or []
    )

    pinned_run = None
    if run_id is not None:
        view_args = ["run", "view", str(run_id), f"--repo={_REPO}", f"--json={_E2E_RUN_FIELDS}"]
        pinned_run = _gh_json(ctx, view_args)

    return _evaluate_runs(ctx, ctx.e2e_runs, pinned_run, open_issues)


def _clean_log_line(raw_line: str) -> str:
    """Strips Actions timestamps and ANSI sequences and bounds per-line character length."""
    without_ts = _LOG_PREFIX_RE.sub("", raw_line)
    cleaned = _ANSI_RE.sub("", without_ts).strip()
    if len(cleaned) <= _MAX_EXCERPT_CHARS:
        return cleaned
    return f"{cleaned[:_MAX_EXCERPT_CHARS]}..."


def _extract_error_excerpt(log_text: str) -> list[str]:
    """Extracts bounded `##[error]`/`::error` context lines (or trailing lines) from a job log."""
    cleaned_lines = [_clean_log_line(raw) for raw in log_text.splitlines()]
    non_empty = [line for line in cleaned_lines if line]
    if not non_empty:
        return []

    error_indices = [
        idx for idx, line in enumerate(non_empty) if "##[error]" in line or "::error" in line
    ]
    if not error_indices:
        return non_empty[-_MAX_EXCERPT_LINES:]

    context_lines: list[str] = []
    for idx in error_indices:
        start = max(0, idx - _ERROR_CONTEXT_LINES)
        context_lines.extend(non_empty[start : idx + 1])
    deduped = list(dict.fromkeys(context_lines))
    return deduped[-_MAX_EXCERPT_LINES:]


def _save_job_log(ctx: _TriageContext, job_id: int) -> tuple[str | None, list[str]]:
    """Downloads raw log output for a failed job and returns `(log_path, error_excerpt)`."""
    res = _run_quiet(["gh", "api", f"repos/{_REPO}/actions/jobs/{job_id}/logs"], ctx.repo_dir)
    if res.returncode != 0:
        ctx.warnings.append(f"Failed to download log for job {job_id}: {res.stderr.strip()}")
        return None, []

    excerpt = _extract_error_excerpt(res.stdout)
    dest = ctx.scratch_dir / f"job_{job_id}.log"
    if dest.is_symlink():
        ctx.warnings.append(f"Skipped symlink log path '{dest}'.")
        return None, excerpt

    dest.write_text(res.stdout, encoding="utf-8")
    return str(dest), excerpt


def _build_failed_job_entry(
    ctx: _TriageContext,
    job: dict[str, Any],
    bad_conclusions: set[str],
    name_map: dict[str, str],
) -> dict[str, Any]:
    """Formats a single failed job record with its saved log path and bounded error excerpt."""
    job_id = int(job["id"])
    job_name = str(job.get("name") or "")
    failed_steps = [
        step.get("name", "")
        for step in job.get("steps", [])
        if step.get("conclusion") in bad_conclusions
    ]
    log_path, error_excerpt = _save_job_log(ctx, job_id)
    return {
        "job_id": job_id,
        "name": job_name,
        "example_id": name_map.get(job_name, ""),
        "conclusion": job.get("conclusion") or "",
        "failed_steps": failed_steps,
        "log_path": log_path,
        "error_excerpt": error_excerpt,
        "result": None,
    }


def _collect_failed_jobs(
    ctx: _TriageContext, run_id: int, run_conclusion: str
) -> list[dict[str, Any]]:
    """Lists jobs on `run_id`, downloads logs for failed jobs, and maps matrix example IDs."""
    payload = _gh_json(ctx, ["api", f"repos/{_REPO}/actions/runs/{run_id}/jobs?per_page=100"]) or {}
    name_map = _load_matrix_name_map(ctx.repo_dir)
    bad_conclusions = set(_FAILURE_CONCLUSIONS) | (
        {"cancelled"} if run_conclusion == "cancelled" else set()
    )
    return [
        _build_failed_job_entry(ctx, job, bad_conclusions, name_map)
        for job in payload.get("jobs", [])
        if job.get("conclusion") in bad_conclusions
    ]


def _download_single_artifact(ctx: _TriageContext, run_id: int, name: str) -> str | None:
    """Safely prepares the destination path and downloads one Actions artifact."""
    dest = ctx.scratch_dir / name
    if dest.is_symlink():
        return None
    if dest.is_dir():
        shutil.rmtree(dest)
    elif dest.exists():
        dest.unlink()

    cmd = ["gh", "run", "download", str(run_id), f"--repo={_REPO}", "-n", name, "-D", str(dest)]
    res = _run_quiet(cmd, ctx.repo_dir)
    if res.returncode != 0:
        ctx.warnings.append(f"Failed to download artifact '{name}': {res.stderr.strip()}")
        return None
    return str(dest)


def _download_artifacts(
    ctx: _TriageContext, run_id: int, exact_names: set[str], name_prefix: str = ""
) -> list[str]:
    """Downloads non-expired artifacts matching `exact_names` or `name_prefix`."""
    endpoint = f"repos/{_REPO}/actions/runs/{run_id}/artifacts?per_page=100"
    payload = _gh_json(ctx, ["api", endpoint], fatal=False) or {}

    downloaded: list[str] = []
    for artifact in payload.get("artifacts", []):
        name = str(artifact.get("name") or "")
        if artifact.get("expired") is not False or not _ARTIFACT_NAME_RE.match(name):
            continue
        if name not in exact_names and not (name_prefix and name.startswith(name_prefix)):
            continue

        saved_path = _download_single_artifact(ctx, run_id, name)
        if saved_path is not None:
            downloaded.append(saved_path)
    return downloaded


def _read_result_artifact(
    ctx: _TriageContext, example_id: str, downloaded_dirs: set[str]
) -> dict[str, Any] | None:
    """Safely parses `<scratch_dir>/result-<example_id>/<example_id>.json` when freshly downloaded."""
    if not example_id or not _ARTIFACT_NAME_RE.match(example_id):
        return None

    artifact_dir = ctx.scratch_dir / f"result-{example_id}"
    if str(artifact_dir) not in downloaded_dirs or artifact_dir.is_symlink():
        return None

    result_file = artifact_dir / f"{example_id}.json"
    if result_file.is_symlink():
        ctx.warnings.append(f"Skipped symlink result artifact '{result_file}'.")
        return None
    if not result_file.exists():
        return None

    try:
        file_stat = result_file.lstat()
        if not stat.S_ISREG(file_stat.st_mode) or file_stat.st_size > _MAX_RESULT_BYTES:
            ctx.warnings.append(f"Invalid or oversized result JSON '{result_file}'.")
            return None
        parsed = json.loads(result_file.read_text(encoding="utf-8", errors="replace"))
    except (OSError, json.JSONDecodeError) as exc:
        ctx.warnings.append(f"Failed to parse result JSON '{result_file}': {exc}")
        return None

    if not isinstance(parsed, dict):
        ctx.warnings.append(f"Expected JSON object in '{result_file}'.")
        return None
    return parsed


def _attach_job_results(
    ctx: _TriageContext, failed_jobs: list[dict[str, Any]], e2e_artifacts: list[str]
) -> None:
    """Populates `job['result']` on failed matrix jobs from downloaded `result-*` artifacts."""
    downloaded_dirs = set(e2e_artifacts)
    for job in failed_jobs:
        job["result"] = _read_result_artifact(ctx, job["example_id"], downloaded_dirs)


def _find_prior_green_e2e_sha(target_run: dict[str, Any], e2e_runs: list[dict[str, Any]]) -> str:
    """Finds the `headSha` of the most recent scheduled green E2E run before `target_run`."""
    cutoff = str(target_run.get("createdAt") or "")
    for run in e2e_runs:
        created_at = str(run.get("createdAt") or "")
        if (
            run.get("status") == "completed"
            and run.get("conclusion") == "success"
            and run.get("event") == "schedule"
            and (not cutoff or created_at < cutoff)
        ):
            sha = str(run.get("headSha") or "")
            return sha if _SHA_RE.match(sha) else ""
    return ""


def _collect_recent_commits(ctx: _TriageContext, target_run: dict[str, Any]) -> list[str]:
    """Collects commits in `<prior_green_e2e_sha>..<target_sha>` or the last 20 at `target_sha`."""
    target_sha = str(target_run.get("headSha") or "")
    if not _SHA_RE.match(target_sha):
        ctx.warnings.append("Invalid commit SHA on target E2E run; recent commits unknown.")
        return []

    prior_sha = _find_prior_green_e2e_sha(target_run, ctx.e2e_runs)
    if prior_sha and prior_sha != target_sha:
        range_cmd = [
            "git",
            "log",
            "--oneline",
            f"-n{_RANGE_COMMIT_LIMIT}",
            f"{prior_sha}..{target_sha}",
            "--",
        ]
        range_res = _run_quiet(range_cmd, ctx.repo_dir)
        range_lines = [line for line in range_res.stdout.splitlines() if line.strip()]
        if range_res.returncode == 0 and range_lines:
            return range_lines

    fallback_cmd = ["git", "log", "--oneline", f"-n{_RECENT_COMMIT_LIMIT}", target_sha, "--"]
    fallback_res = _run_quiet(fallback_cmd, ctx.repo_dir)
    if fallback_res.returncode != 0:
        err = fallback_res.stderr.strip() or "git log failed"
        ctx.warnings.append(f"Recent commits git log ({target_sha}) failed: {err}")
        return []
    return [line for line in fallback_res.stdout.splitlines() if line.strip()]


def _inspect_git_skew(
    ctx: _TriageContext, last_green_build: dict[str, Any] | None, target_run: dict[str, Any]
) -> tuple[list[str], list[str]]:
    """Computes undeployed commits and cluster-deployed files between build and E2E SHAs."""
    if not last_green_build:
        ctx.warnings.append("No prior green nightly-build.yml run found; rollout skew unknown.")
        return [], []

    base_sha = str(last_green_build.get("headSha") or "")
    e2e_sha = str(target_run.get("headSha") or "")
    if not (_SHA_RE.match(base_sha) and _SHA_RE.match(e2e_sha)):
        ctx.warnings.append("Invalid commit SHA on build or E2E run; rollout skew unknown.")
        return [], []
    if base_sha == e2e_sha:
        return [], []

    commit_range = f"{base_sha}..{e2e_sha}"
    log_res = _run_quiet(["git", "log", "--oneline", commit_range, "--"], ctx.repo_dir)
    diff_res = _run_quiet(["git", "diff", "--name-only", commit_range, "--"], ctx.repo_dir)
    if log_res.returncode != 0 or diff_res.returncode != 0:
        err = (log_res.stderr or diff_res.stderr).strip() or "git command failed"
        ctx.warnings.append(f"Rollout skew git inspect ({commit_range}) failed: {err}")
        return [], []

    undeployed_commits = [line for line in log_res.stdout.splitlines() if line.strip()]
    cluster_files = [
        path for path in diff_res.stdout.splitlines() if path.startswith(_CLUSTER_DEPLOYED_PREFIXES)
    ]
    return undeployed_commits, cluster_files


def _compute_rollout_skew(
    ctx: _TriageContext, target_run: dict[str, Any], build_runs: list[dict[str, Any]]
) -> dict[str, Any]:
    """Finds the active and last green builds prior to `target_run` and inspects git skew."""
    completed = [run for run in build_runs if run.get("status") == "completed"]
    cutoff = str(target_run.get("createdAt") or "")
    prior_builds = [
        run for run in completed if not cutoff or str(run.get("createdAt") or "") <= cutoff
    ]
    if completed and not prior_builds:
        ctx.warnings.append(
            f"No prior nightly-build.yml run found in {_BUILD_RUN_LIMIT}-run lookback window."
        )

    active_build = next(iter(prior_builds), None)
    last_green = next((run for run in prior_builds if run.get("conclusion") == "success"), None)
    undeployed_commits, cluster_files = _inspect_git_skew(ctx, last_green, target_run)
    return {
        "active_build_run": active_build,
        "last_green_build_run": last_green,
        "undeployed_commits": undeployed_commits,
        "cluster_deployed_files": cluster_files,
    }


def _download_failed_build_diagnostics(
    ctx: _TriageContext, active_build: dict[str, Any] | None
) -> list[str]:
    """Downloads `deploy-gke-diagnostics` when the active prior nightly build failed."""
    if not active_build or active_build.get("conclusion") not in _FAILURE_CONCLUSIONS:
        return []
    return _download_artifacts(ctx, int(active_build["databaseId"]), {"deploy-gke-diagnostics"})


def _enrich_with_failure_diagnostics(
    ctx: _TriageContext, report: dict[str, Any], target_run: dict[str, Any]
) -> dict[str, Any]:
    """Populates failed jobs, parsed result JSON, recent commits, and cluster rollout skew."""
    run_id = int(target_run["databaseId"])
    failed_jobs = _collect_failed_jobs(ctx, run_id, str(target_run.get("conclusion") or ""))
    result_names = {f"result-{job['example_id']}" for job in failed_jobs if job["example_id"]}
    e2e_artifacts = _download_artifacts(ctx, run_id, result_names, "ui-screenshots-")
    _attach_job_results(ctx, failed_jobs, e2e_artifacts)
    recent_commits = _collect_recent_commits(ctx, target_run)

    build_runs = _list_workflow_runs(
        ctx, "nightly-build.yml", _BUILD_RUN_LIMIT, _BUILD_RUN_FIELDS, fatal=False
    )
    rollout_skew = _compute_rollout_skew(ctx, target_run, build_runs)
    build_diagnostics = _download_failed_build_diagnostics(ctx, rollout_skew["active_build_run"])

    report.update(
        failed_jobs=failed_jobs,
        downloaded_artifacts=[*e2e_artifacts, *build_diagnostics],
        recent_commits=recent_commits,
        rollout_skew=rollout_skew,
        scratch_dir=str(ctx.scratch_dir),
    )
    return report


def triage_nightly_run(args: argparse.Namespace) -> dict[str, Any]:
    """Discovers and triages the target Nightly Examples E2E run on main."""
    if args.run_id is not None and args.run_id <= 0:
        raise HandledError("--run-id must be >= 1.", "Pass a positive GitHub Actions run ID.")

    ctx = _TriageContext(repo_dir=Path(args.repo_dir) if args.repo_dir else None)
    report = _discover_nightly_status(ctx, args.run_id)
    target_run = report["target_run"]
    if target_run is None or not _is_failed_run(target_run):
        return report

    run_id = int(target_run["databaseId"])
    default_scratch = Path(os.environ.get("ARTIFACT_DIR") or "/tmp") / f"nightly_triage_{run_id}"
    ctx.scratch_dir = Path(args.scratch_dir) if args.scratch_dir else default_scratch
    if ctx.scratch_dir.is_symlink():
        raise HandledError(
            f"Refusing to use symlink scratch directory '{ctx.scratch_dir}'.",
            "Pass a real directory path via --scratch-dir.",
        )
    ctx.scratch_dir.mkdir(parents=True, exist_ok=True)
    return _enrich_with_failure_diagnostics(ctx, report, target_run)


def create_parser() -> argparse.ArgumentParser:
    """Constructs the CLI argument parser for nightly run triage."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--run-id", type=int, default=None, help="Specific Actions run ID to triage."
    )
    parser.add_argument(
        "--scratch-dir", default="", help="Output directory for logs and artifacts."
    )
    parser.add_argument("--repo-dir", default=".", help="Local git repository root.")
    return parser


def main() -> None:
    """CLI entrypoint executing `triage_nightly_run` via `tool_utils.run_tool`."""
    run_tool(create_parser(), triage_nightly_run)


if __name__ == "__main__":
    main()
