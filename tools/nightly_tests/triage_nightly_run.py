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

"""One-shot CLI helper to discover and triage broken Nightly Examples E2E runs."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any, NamedTuple, TypeGuard

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests.examples_import_smoke import parse_matrix_items, resolve_workflow_path

_REPO_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$")
_REF_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]*$")
_ART_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
_FAIL_SET = frozenset({"failure", "timed_out", "startup_failure", "action_required"})
_OK_SET = frozenset({"success", "skipped", "neutral"})
_DEPLOY_DIRS = tuple(f"deploy/{d}/" for d in ["helm", "ctk_v2", "addons", "clickhouse", "profiles"])
_CLUSTER_DIRS = ("cmd/", "internal/", "pkg/", "api/", *_DEPLOY_DIRS)
_E2E_JSON = "databaseId,status,conclusion,headSha,event,createdAt,url"
_BUILD_JSON = "databaseId,status,conclusion,headSha,createdAt,url"
_ISS_JSON = "number,title,url,createdAt"


class _TriageCtx(NamedTuple):
    repo: str
    repo_dir: Path | None
    scratch_dir: Path
    warnings: list[str]


def _quiet(cmd: list[str], cwd: Path | None) -> subprocess.CompletedProcess[str]:
    kw: dict[str, Any] = {"cwd": cwd, "capture_output": True, "check": False, "log_cmd": False}
    return run_command(cmd, errors="replace", **kw)


def _gh_json(args: list[str], repo_dir: Path | None, warnings: list[str] | None = None) -> Any:
    res = _quiet(["gh", *args], repo_dir)
    if res.returncode == 0 and res.stdout.strip():
        try:
            return json.loads(res.stdout)
        except json.JSONDecodeError as exc:
            msg = f"Invalid JSON from 'gh {' '.join(args[:3])}': {exc}"
    else:
        msg = f"'gh {' '.join(args[:3])}' failed: {res.stderr.strip() or f'exit {res.returncode}'}"
    if warnings is None:
        raise HandledError(msg, "Verify GitHub CLI authentication ('gh auth status') and network.")
    warnings.append(msg)
    return None


def _validate_inputs(args: argparse.Namespace) -> tuple[str, str, Path | None]:
    for binary, hint in (("gh", "gh auth login"), ("git", "Install git on PATH")):
        if not shutil.which(binary):
            raise HandledError(f"Required CLI '{binary}' was not found in PATH.", hint)
    repo, branch = args.repo.strip(), args.branch.strip()
    if not _REPO_RE.match(repo):
        raise HandledError(f"Invalid --repo '{args.repo}'.", "Use OWNER/REPO format.")
    bad_ref = branch.startswith("origin/") or branch.endswith("/") or not _REF_RE.match(branch)
    if bad_ref or ".." in branch or "//" in branch:
        raise HandledError(f"Invalid --branch '{args.branch}'.", "Omit 'origin/' prefix.")
    if (args.run_id is not None and args.run_id <= 0) or args.limit < 1:
        raise HandledError("--run-id and --limit must be >= 1.", "Pass positive integers.")
    return repo, branch, (Path(args.repo_dir) if getattr(args, "repo_dir", None) else None)


def _load_matrix_name_map(raw_dir: str | None) -> dict[str, str]:
    wf = resolve_workflow_path(raw_dir)
    items = parse_matrix_items(wf.read_text(encoding="utf-8")) if wf.is_file() else []
    return {f"Run & Verify: {i['name']}": i["id"] for i in items if i.get("name") and i.get("id")}


def _is_newer(a: Any, b: Any) -> bool:
    return bool(b and (o := b.get("createdAt")) and ((a or {}).get("createdAt") or "") >= str(o))


def _is_failed_run(run: Any) -> TypeGuard[dict[str, Any]]:
    return bool(run and run.get("status") == "completed" and run.get("conclusion") not in _OK_SET)


def _select_target_run(runs: list[Any], pinned: Any, auto: bool, warns: list[str]) -> Any:
    done = [r for r in runs if r.get("status") == "completed"]
    greens = [r for r in done if r.get("conclusion") == "success"]
    sched_green = next((r for r in greens if r.get("event") == "schedule"), None)
    latest_fail = next((r for r in done if r.get("conclusion") not in _OK_SET), None)
    active_fail = None if _is_newer(sched_green, latest_fail) else latest_fail
    target = active_fail if auto else pinned
    sup = sched_green if (_is_failed_run(target) and _is_newer(sched_green, target)) else None
    manual = next((r for r in greens if r.get("event") != "schedule"), None)
    if target is None and auto and not done:
        warns.append("No completed nightly-examples-e2e.yml runs found.")
    elif _is_failed_run(target) and sup is None and manual and _is_newer(manual, target):
        ev, mid = manual.get("event") or "manual", manual.get("databaseId")
        warns.append(f"Newer green {ev} run {mid} (possibly filtered) not treated as superseding.")
    return (done[0] if done else None), sup, target, (active_fail is not None)


def _collect_failed_jobs(ctx: _TriageCtx, run_id: int, concl: str, nmap: dict[str, str]) -> Any:
    ep = f"repos/{ctx.repo}/actions/runs/{run_id}/jobs?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir) or {}
    bad = _FAIL_SET | ({"cancelled"} if concl == "cancelled" else set())
    failed_jobs: list[dict[str, Any]] = []
    for job in payload.get("jobs", []):
        if job.get("conclusion") not in bad:
            continue
        jid, name = int(job["id"]), str(job.get("name") or "")
        ex_id = nmap.get(name, "")
        res = _quiet(["gh", "api", f"repos/{ctx.repo}/actions/jobs/{jid}/logs"], ctx.repo_dir)
        log_path = str(dest := ctx.scratch_dir / f"job_{jid}.log") if res.returncode == 0 else None
        if res.returncode == 0:
            dest.write_text(res.stdout, encoding="utf-8")
        else:
            ctx.warnings.append(f"Failed to download log for job {jid}: {res.stderr.strip()}")
        steps = [s.get("name", "") for s in job.get("steps", []) if s.get("conclusion") in bad]
        head = {"job_id": jid, "name": name, "example_id": ex_id, "failed_steps": steps}
        failed_jobs.append(head | {"conclusion": job.get("conclusion") or "", "log_path": log_path})
    return failed_jobs


def _download_artifacts(ctx: _TriageCtx, run_id: int, names: set[str], pfx: str = "") -> list[str]:
    ep = f"repos/{ctx.repo}/actions/runs/{run_id}/artifacts?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir, ctx.warnings) or {}
    downloaded, root = [], ctx.scratch_dir.resolve()
    for art in payload.get("artifacts", []):
        name = str(art.get("name") or "")
        if art.get("expired") is not False or not _ART_RE.match(name):
            continue
        if name not in names and not (pfx and name.startswith(pfx)):
            continue
        raw_dest = ctx.scratch_dir / name
        dest = raw_dest.resolve()
        if raw_dest.is_symlink() or dest == root or not dest.is_relative_to(root):
            continue
        shutil.rmtree(dest, ignore_errors=True)
        dl_cmd = [*f"gh run download {run_id} --repo {ctx.repo} -n {name} -D".split(), str(dest)]
        res = _quiet(dl_cmd, ctx.repo_dir)
        if res.returncode == 0:
            downloaded.append(str(dest))
        else:
            ctx.warnings.append(f"Failed to download artifact '{name}': {res.stderr.strip()}")
    return downloaded


def _compute_rollout_skew(ctx: _TriageCtx, target: dict[str, Any], builds: list[Any]) -> Any:
    done = [b for b in builds if b.get("status") == "completed"]
    cutoff = str(target.get("createdAt") or "")
    prior = [b for b in done if not cutoff or str(b.get("createdAt") or "") <= cutoff]
    if done and not prior:
        ctx.warnings.append("No prior nightly-build.yml run found in 15-run lookback window.")
    active = next(iter(prior), None)
    green = next((b for b in prior if b.get("conclusion") == "success"), None)
    aid = int(active["databaseId"]) if active and active.get("conclusion") in _FAIL_SET else 0
    diag = _download_artifacts(ctx, aid, {"deploy-gke-diagnostics"}) if aid else []
    base_sha, e2e_sha = str((green or {}).get("headSha") or ""), str(target.get("headSha") or "")
    commits, cluster_files = [], []
    if not green:
        ctx.warnings.append("No prior green nightly-build.yml run found; rollout skew unknown.")
    elif not (_REF_RE.match(base_sha) and _REF_RE.match(e2e_sha)) or ".." in base_sha + e2e_sha:
        ctx.warnings.append("Invalid commit SHA on build or E2E run; rollout skew unknown.")
    elif base_sha != e2e_sha:
        rng = f"{base_sha}..{e2e_sha}"
        log_r = _quiet(["git", "log", "--oneline", rng], ctx.repo_dir)
        diff_r = _quiet(["git", "diff", "--name-only", rng], ctx.repo_dir)
        if log_r.returncode == 0 and diff_r.returncode == 0:
            commits = [ln for ln in log_r.stdout.splitlines() if ln.strip()]
            cluster_files = [f for f in diff_r.stdout.splitlines() if f.startswith(_CLUSTER_DIRS)]
        else:
            err = (log_r.stderr or diff_r.stderr).strip() or "git command failed"
            ctx.warnings.append(f"Rollout skew git inspect ({rng}) failed: {err}")
    skew = {"active_build_run": active, "last_green_build_run": green}
    return skew | {"undeployed_commits": commits, "cluster_deployed_files": cluster_files}, diag


def _report(e2e: Any, iss: list[Any], warns: list[str], **kw: Any) -> dict[str, Any]:
    lat, sup, tgt, is_fail = e2e
    if tgt is None:
        status = "no_completed_runs" if lat is None else "healthy"
    elif not _is_failed_run(tgt):
        status = "in_progress" if tgt.get("status") != "completed" else "healthy"
    else:
        status = "superseded" if (sup and not is_fail) else "failing"
    base = {"status_summary": status, "is_latest_e2e_failing": is_fail, "target_run": tgt}
    refs = {"latest_completed_e2e_run": lat, "superseded_by_green_run": sup, "open_issues": iss}
    empty: dict[str, Any] = {"failed_jobs": [], "downloaded_artifacts": [], "rollout_skew": None}
    return base | refs | empty | {"scratch_dir": None, "warnings": warns} | kw


def triage_nightly_run(args: argparse.Namespace) -> dict[str, Any]:
    """Executes Stage 1 & Stage 2 nightly discovery, artifact collection, and skew analysis."""
    repo, branch, repo_dir = _validate_inputs(args)
    warnings: list[str] = []
    if (fr := _quiet(["git", "fetch", "--", "origin", branch], repo_dir)).returncode != 0:
        warnings.append(f"git fetch origin {branch} failed: {fr.stderr.strip()}")
    base_q = f"run list --repo={repo} --branch={branch}"
    e2e_q = f"{base_q} --workflow=nightly-examples-e2e.yml --limit={args.limit} --json={_E2E_JSON}"
    e2e_runs = _gh_json(e2e_q.split(), repo_dir) or []
    iss_q = f"issue list --repo={repo} --label=nightly-failure --state=open --json={_ISS_JSON}"
    issues = _gh_json(iss_q.split(), repo_dir, warnings) or []
    view_q = f"run view {args.run_id} --repo={repo} --json={_E2E_JSON}"
    pinned = _gh_json(view_q.split(), repo_dir) if args.run_id is not None else None
    e2e = _select_target_run(e2e_runs, pinned, args.run_id is None, warnings)
    if not _is_failed_run(target := e2e[2]):
        return _report(e2e, issues, warnings)
    run_id = int(target["databaseId"])
    default_dir = Path(os.environ.get("ARTIFACT_DIR") or "/tmp") / f"nightly_triage_{run_id}"
    scratch = Path(args.scratch_dir) if args.scratch_dir else default_dir
    scratch.mkdir(parents=True, exist_ok=True)
    ctx = _TriageCtx(repo, repo_dir, scratch, warnings)
    concl = str(target.get("conclusion") or "")
    jobs = _collect_failed_jobs(ctx, run_id, concl, _load_matrix_name_map(args.repo_dir))
    res_names = {f"result-{j['example_id']}" for j in jobs if j["example_id"]}
    arts = _download_artifacts(ctx, run_id, res_names, "ui-screenshots-")
    bld_q = f"{base_q} --workflow=nightly-build.yml --limit=15 --json={_BUILD_JSON}"
    builds = _gh_json(bld_q.split(), repo_dir, warnings) or []
    skew, diag = _compute_rollout_skew(ctx, target, builds)
    bundle = {"failed_jobs": jobs, "downloaded_artifacts": [*arts, *diag], "rollout_skew": skew}
    return _report(e2e, issues, warnings, **bundle, scratch_dir=str(scratch))


def create_parser() -> argparse.ArgumentParser:
    """Constructs the CLI argument parser for nightly run triage."""
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--run-id", type=int, default=None, help="Specific Actions run ID to triage.")
    p.add_argument("--repo", default="google/xmc", help="GitHub OWNER/REPO.")
    p.add_argument("--branch", default="main", help="Target branch name on origin.")
    p.add_argument("--scratch-dir", default="", help="Output directory for logs and artifacts.")
    p.add_argument("--limit", type=int, default=10, help="Number of recent E2E runs to inspect.")
    p.add_argument("--repo-dir", default=".", help="Local git repository root.")
    return p


def main() -> None:
    """CLI entrypoint."""
    run_tool(create_parser(), triage_nightly_run)


if __name__ == "__main__":
    main()
