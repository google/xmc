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

"""One-shot CLI helper to discover and triage broken Nightly Examples E2E runs on main."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
from functools import partial
from pathlib import Path
from typing import Any, NamedTuple

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests.examples_import_smoke import parse_matrix_items, resolve_workflow_path

_REPO, _BRANCH, _E2E_LIMIT, _BUILD_LIMIT = "google/xmc", "main", 10, 15
_SHA_RE = re.compile(r"^[0-9a-fA-F]{7,40}$")
_ART_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
_FAIL_SET = frozenset({"failure", "timed_out", "startup_failure", "action_required"})
_OK_SET = frozenset({"success", "skipped", "neutral"})
_ROOT_DIRS = ("cmd/", "internal/", "pkg/", "api/")
_DEPLOY_DIRS = ("deploy/helm/", "deploy/ctk_v2/", "deploy/addons/", "deploy/clickhouse/")
_CLUSTER_DIRS = (*_ROOT_DIRS, *_DEPLOY_DIRS, "deploy/profiles/")
_E2E_JSON = "databaseId,status,conclusion,headSha,event,createdAt,url"
_BUILD_JSON = "databaseId,status,conclusion,headSha,createdAt,url"
_ISS_JSON = "number,title,url,createdAt"
_CLI_HINT = "Ensure 'gh' and 'git' CLIs are on PATH and run 'gh auth login'."


class _TriageCtx(NamedTuple):
    repo_dir: Path | None
    scratch_dir: Path
    warnings: list[str]


def _quiet(cmd: list[str], cwd: Path | None) -> subprocess.CompletedProcess[str]:
    run = partial(run_command, cwd=cwd, capture_output=True, check=False, log_cmd=False)
    return run(cmd, errors="replace", handled_instruction=_CLI_HINT)


def _gh_json(args: list[str], repo_dir: Path | None, warns: list[str] | None = None) -> Any:
    res, label = _quiet(["gh", *args], repo_dir), f"gh {' '.join(args[:3])}"
    if res.returncode == 0:
        try:
            return json.loads(res.stdout)
        except json.JSONDecodeError as exc:
            msg = f"Invalid JSON from '{label}': {exc}"
    else:
        msg = f"'{label}' failed: {res.stderr.strip() or f'exit {res.returncode}'}"
    if warns is None:
        raise HandledError(msg, "Verify GitHub CLI authentication ('gh auth status') and network.")
    warns.append(msg)
    return None


def _list_runs(
    wf: str, limit: int, fields: str, repo_dir: Path | None, warns: list[str] | None = None
) -> list[dict[str, Any]]:
    flags = [f"--repo={_REPO}", f"--branch={_BRANCH}", f"--workflow={wf}", f"--limit={limit}"]
    return _gh_json(["run", "list", *flags, f"--json={fields}"], repo_dir, warns) or []


def _load_matrix_name_map(repo_dir: Path | None) -> dict[str, str]:
    wf = resolve_workflow_path(repo_dir)
    items = parse_matrix_items(wf.read_text(encoding="utf-8")) if wf.is_file() else []
    return {f"Run & Verify: {i['name']}": i["id"] for i in items if i.get("name") and i.get("id")}


def _is_newer(run: dict[str, Any] | None, other: dict[str, Any] | None) -> bool:
    r_ts, o_ts = (run or {}).get("createdAt"), (other or {}).get("createdAt")
    return bool(r_ts and o_ts and str(r_ts) >= str(o_ts))


def _is_failed_run(run: dict[str, Any] | None) -> bool:
    return bool(run and run.get("status") == "completed" and run.get("conclusion") not in _OK_SET)


def _evaluate_runs(
    runs: list[dict[str, Any]],
    pinned: dict[str, Any] | None,
    issues: list[dict[str, Any]],
    warns: list[str],
) -> dict[str, Any]:
    done = [r for r in runs if r.get("status") == "completed"]
    greens = [r for r in done if r.get("conclusion") == "success"]
    sched_green = next((r for r in greens if r.get("event") == "schedule"), None)
    latest_fail = next((r for r in done if r.get("conclusion") not in _OK_SET), None)
    target = (
        (None if _is_newer(sched_green, latest_fail) else latest_fail) if pinned is None else pinned
    )
    sup = sched_green if _is_failed_run(target) and _is_newer(sched_green, target) else None
    manual = next((r for r in greens if r.get("event") != "schedule"), None)
    if pinned is None and not done:
        warns.append("No completed nightly-examples-e2e.yml runs found.")
    if _is_failed_run(target) and sup is None and manual and _is_newer(manual, target):
        ev, mid = manual.get("event") or "manual", manual.get("databaseId")
        warns.append(f"Newer green {ev} run {mid} (possibly filtered) not treated as superseding.")
    if target is None:
        status = "healthy" if done else "no_completed_runs"
    elif target.get("status") != "completed":
        status = "in_progress"
    else:
        status = ("superseded" if sup else "failing") if _is_failed_run(target) else "healthy"
    out: dict[str, Any] = {"status_summary": status, "target_run": target, "open_issues": issues}
    out.update(superseded_by_green_run=sup, failed_jobs=[], downloaded_artifacts=[])
    return out | {"rollout_skew": None, "scratch_dir": None, "warnings": warns}


def _collect_failed_jobs(
    ctx: _TriageCtx, run_id: int, concl: str, nmap: dict[str, str]
) -> list[dict[str, Any]]:
    ep = f"repos/{_REPO}/actions/runs/{run_id}/jobs?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir) or {}
    bad = _FAIL_SET | ({"cancelled"} if concl == "cancelled" else set())
    failed_jobs: list[dict[str, Any]] = []
    for job in payload.get("jobs", []):
        if job.get("conclusion") not in bad:
            continue
        jid, name = int(job["id"]), str(job.get("name") or "")
        res = _quiet(["gh", "api", f"repos/{_REPO}/actions/jobs/{jid}/logs"], ctx.repo_dir)
        dest, log_path = ctx.scratch_dir / f"job_{jid}.log", None
        if res.returncode != 0:
            ctx.warnings.append(f"Failed to download log for job {jid}: {res.stderr.strip()}")
        elif dest.is_symlink():
            ctx.warnings.append(f"Skipped symlink log path '{dest}'.")
        else:
            dest.write_text(res.stdout, encoding="utf-8")
            log_path = str(dest)
        steps = [s.get("name", "") for s in job.get("steps", []) if s.get("conclusion") in bad]
        head = {"job_id": jid, "name": name, "example_id": nmap.get(name, "")}
        tail = {"conclusion": job.get("conclusion") or "", "failed_steps": steps}
        failed_jobs.append(head | tail | {"log_path": log_path})
    return failed_jobs


def _download_artifacts(ctx: _TriageCtx, run_id: int, names: set[str], pfx: str = "") -> list[str]:
    ep = f"repos/{_REPO}/actions/runs/{run_id}/artifacts?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir, ctx.warnings) or {}
    downloaded: list[str] = []
    for art in payload.get("artifacts", []):
        name = str(art.get("name") or "")
        if art.get("expired") is not False or not _ART_RE.match(name):
            continue
        if name not in names and not (pfx and name.startswith(pfx)):
            continue
        dest = ctx.scratch_dir / name
        if dest.is_symlink():
            continue
        shutil.rmtree(dest) if dest.is_dir() else dest.unlink(missing_ok=True)
        dl = ["run", "download", str(run_id), f"--repo={_REPO}", "-n", name, "-D", str(dest)]
        if (res := _quiet(["gh", *dl], ctx.repo_dir)).returncode == 0:
            downloaded.append(str(dest))
        else:
            ctx.warnings.append(f"Failed to download artifact '{name}': {res.stderr.strip()}")
    return downloaded


def _inspect_git_skew(
    repo_dir: Path | None, green: dict[str, Any] | None, target: dict[str, Any], warns: list[str]
) -> tuple[list[str], list[str]]:
    if not green:
        warns.append("No prior green nightly-build.yml run found; rollout skew unknown.")
        return [], []
    base_sha, e2e_sha = str(green.get("headSha") or ""), str(target.get("headSha") or "")
    if not (_SHA_RE.match(base_sha) and _SHA_RE.match(e2e_sha)):
        warns.append("Invalid commit SHA on build or E2E run; rollout skew unknown.")
        return [], []
    if base_sha == e2e_sha:
        return [], []
    rng = f"{base_sha}..{e2e_sha}"
    log_r = _quiet(["git", "log", "--oneline", rng], repo_dir)
    diff_r = _quiet(["git", "diff", "--name-only", rng], repo_dir)
    if log_r.returncode != 0 or diff_r.returncode != 0:
        err = (log_r.stderr or diff_r.stderr).strip() or "git command failed"
        warns.append(f"Rollout skew git inspect ({rng}) failed: {err}")
        return [], []
    commits = [ln for ln in log_r.stdout.splitlines() if ln.strip()]
    cluster_files = [f for f in diff_r.stdout.splitlines() if f.startswith(_CLUSTER_DIRS)]
    return commits, cluster_files


def _compute_rollout_skew(
    target: dict[str, Any], builds: list[dict[str, Any]], repo_dir: Path | None, warns: list[str]
) -> dict[str, Any]:
    done = [b for b in builds if b.get("status") == "completed"]
    cutoff = str(target.get("createdAt") or "")
    prior = [b for b in done if not cutoff or str(b.get("createdAt") or "") <= cutoff]
    if done and not prior:
        warns.append(f"No prior nightly-build.yml run found in {_BUILD_LIMIT}-run lookback window.")
    active = next(iter(prior), None)
    green = next((b for b in prior if b.get("conclusion") == "success"), None)
    commits, cluster_files = _inspect_git_skew(repo_dir, green, target, warns)
    res = {"active_build_run": active, "last_green_build_run": green}
    return res | {"undeployed_commits": commits, "cluster_deployed_files": cluster_files}


def triage_nightly_run(args: argparse.Namespace) -> dict[str, Any]:
    """Executes Stage 1 & Stage 2 nightly discovery, artifact collection, and skew analysis."""
    if args.run_id is not None and args.run_id <= 0:
        raise HandledError("--run-id must be >= 1.", "Pass a positive GitHub Actions run ID.")
    repo_dir = Path(args.repo_dir) if args.repo_dir else None
    warnings: list[str] = []
    if (fr := _quiet(["git", "fetch", "--", "origin", _BRANCH], repo_dir)).returncode != 0:
        warnings.append(f"git fetch origin {_BRANCH} failed: {fr.stderr.strip()}")
    e2e_runs = _list_runs("nightly-examples-e2e.yml", _E2E_LIMIT, _E2E_JSON, repo_dir)
    iss_q = ["issue", "list", f"--repo={_REPO}", "--label=nightly-failure", "--state=open"]
    issues = _gh_json([*iss_q, f"--json={_ISS_JSON}"], repo_dir, warnings) or []
    view_cmd = ["run", "view", str(args.run_id), f"--repo={_REPO}", f"--json={_E2E_JSON}"]
    pinned = _gh_json(view_cmd, repo_dir) if args.run_id is not None else None
    report = _evaluate_runs(e2e_runs, pinned, issues, warnings)
    if not _is_failed_run(target := report["target_run"]):
        return report
    run_id = int(target["databaseId"])
    default_dir = Path(os.environ.get("ARTIFACT_DIR") or "/tmp") / f"nightly_triage_{run_id}"
    scratch = Path(args.scratch_dir) if args.scratch_dir else default_dir
    scratch.mkdir(parents=True, exist_ok=True)
    ctx, concl = _TriageCtx(repo_dir, scratch, warnings), str(target.get("conclusion") or "")
    jobs = _collect_failed_jobs(ctx, run_id, concl, _load_matrix_name_map(repo_dir))
    res_names = {f"result-{j['example_id']}" for j in jobs if j["example_id"]}
    arts = _download_artifacts(ctx, run_id, res_names, "ui-screenshots-")
    builds = _list_runs("nightly-build.yml", _BUILD_LIMIT, _BUILD_JSON, repo_dir, warnings)
    skew, diag = _compute_rollout_skew(target, builds, repo_dir, warnings), []
    if (ab := skew["active_build_run"]) and ab.get("conclusion") in _FAIL_SET:
        diag = _download_artifacts(ctx, int(ab["databaseId"]), {"deploy-gke-diagnostics"})
    details = {"failed_jobs": jobs, "downloaded_artifacts": [*arts, *diag], "rollout_skew": skew}
    return report | details | {"scratch_dir": str(scratch)}


def create_parser() -> argparse.ArgumentParser:
    """Constructs the CLI argument parser for nightly run triage."""
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--run-id", type=int, default=None, help="Specific Actions run ID to triage.")
    p.add_argument("--scratch-dir", default="", help="Output directory for logs and artifacts.")
    p.add_argument("--repo-dir", default=".", help="Local git repository root.")
    return p


def main() -> None:
    """CLI entrypoint."""
    run_tool(create_parser(), triage_nightly_run)


if __name__ == "__main__":
    main()
