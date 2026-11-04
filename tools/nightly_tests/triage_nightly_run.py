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
from collections.abc import Callable
from pathlib import Path
from typing import Any, NamedTuple

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests.examples_import_smoke import parse_matrix_items, resolve_workflow_path

_REPO_RE = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
_REF_RE = re.compile(r"^[A-Za-z0-9._/-]+$")
_ART_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
_FAIL_SET = frozenset({"failure", "timed_out", "startup_failure", "action_required"})
_OK_SET = frozenset({"success", "skipped", "neutral"})
_CLUSTER_DIRS = ("cmd/", "internal/", "pkg/", "api/", "deploy/")
_E2E_JSON = "databaseId,status,conclusion,headSha,event,createdAt,url"
_BUILD_JSON = "databaseId,status,conclusion,headSha,createdAt,url"
_KW: dict[str, Any] = {
    "capture_output": True,
    "check": False,
    "log_cmd": False,
    "log_on_error": False,
}


class _TriageCtx(NamedTuple):
    repo: str
    repo_dir: Path | None
    scratch_dir: Path
    warnings: list[str]


def _quiet(cmd: list[str], cwd: Path | None):
    return run_command(cmd, cwd=cwd, errors="replace", **_KW)


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
    if not _REF_RE.match(branch) or branch.startswith(("-", "origin/")):
        raise HandledError(f"Invalid --branch '{args.branch}'.", "Omit 'origin/' prefix.")
    if args.run_id is not None and args.run_id <= 0:
        raise HandledError("--run-id must be a positive integer.", "Pass a valid Actions run ID.")
    if args.limit < 1:
        raise HandledError("--limit must be >= 1.", "Pass a positive run limit.")
    raw_dir = getattr(args, "repo_dir", None)
    return repo, branch, (Path(raw_dir) if raw_dir else None)


def _load_matrix_name_map(raw_dir: str | None) -> dict[str, str]:
    wf = resolve_workflow_path(raw_dir)
    items = parse_matrix_items(wf.read_text(encoding="utf-8")) if wf.is_file() else []
    return {f"Run & Verify: {i['name']}": i["id"] for i in items if i.get("name") and i.get("id")}


def _select_target_run(repo: str, run_id: int | None, runs: list[Any], cwd: Path | None):
    done = [r for r in runs if r.get("status") == "completed"]
    latest = done[0] if done else None
    if run_id is None:
        return latest, next((r for r in done if r.get("conclusion") not in _OK_SET), None)
    r = _gh_json(["api", f"repos/{repo}/actions/runs/{run_id}"], cwd)
    t = {"databaseId": int(r["id"]), "status": r.get("status", ""), "event": r.get("event", "")}
    t |= {"conclusion": r.get("conclusion", ""), "headSha": r.get("head_sha", "")}
    return latest, t | {"createdAt": r.get("created_at", ""), "url": r.get("html_url", "")}


def _collect_failed_jobs(ctx: _TriageCtx, run_id: int, concl: str, nmap: dict[str, str]):
    ep = f"repos/{ctx.repo}/actions/runs/{run_id}/jobs?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir) or {}
    bad = _FAIL_SET | ({"cancelled"} if concl == "cancelled" else set())
    failed_jobs: list[dict[str, Any]] = []
    failed_ids: set[str] = set()
    for job in payload.get("jobs", []):
        if job.get("conclusion") not in bad:
            continue
        jid, name = int(job["id"]), str(job.get("name", ""))
        ex_id = nmap.get(name, "")
        if ex_id:
            failed_ids.add(ex_id)
        res = _quiet(["gh", "api", f"repos/{ctx.repo}/actions/jobs/{jid}/logs"], ctx.repo_dir)
        log_path: str | None = None
        if res.returncode == 0:
            dest = ctx.scratch_dir / f"job_{jid}.log"
            dest.write_text(res.stdout, encoding="utf-8")
            log_path = str(dest)
        else:
            ctx.warnings.append(f"Failed to download log for job {jid}: {res.stderr.strip()}")
        steps = [s.get("name", "") for s in job.get("steps", []) if s.get("conclusion") in bad]
        row = {"job_id": jid, "name": name, "example_id": ex_id, "failed_steps": steps}
        failed_jobs.append(row | {"conclusion": job.get("conclusion", ""), "log_path": log_path})
    return failed_jobs, failed_ids


def _download_artifacts(ctx: _TriageCtx, run_id: int, wanted: Callable[[str], bool]) -> list[str]:
    ep = f"repos/{ctx.repo}/actions/runs/{run_id}/artifacts?per_page=100"
    payload = _gh_json(["api", ep], ctx.repo_dir, ctx.warnings) or {}
    downloaded, root = [], ctx.scratch_dir.resolve()
    for art in payload.get("artifacts", []):
        name = str(art.get("name", ""))
        if art.get("expired") is not False or not _ART_RE.match(name) or not wanted(name):
            continue
        dest = (ctx.scratch_dir / name).resolve()
        if not dest.is_relative_to(root):
            continue
        dl_cmd = [*f"gh run download {run_id} --repo {ctx.repo} -n {name} -D".split(), str(dest)]
        res = _quiet(dl_cmd, ctx.repo_dir)
        if res.returncode == 0:
            downloaded.append(str(dest))
        else:
            ctx.warnings.append(f"Failed to download artifact '{name}': {res.stderr.strip()}")
    return downloaded


def _compute_rollout_skew(ctx: _TriageCtx, target: dict[str, Any], builds: list[Any]):
    done = [b for b in builds if b.get("status") == "completed"]
    cutoff = target.get("createdAt", "")
    prior = [b for b in done if not cutoff or b.get("createdAt", "") <= cutoff]
    active = prior[0] if prior else (done[0] if done else None)
    green = next((b for b in prior if b.get("conclusion") == "success"), None)
    diag: list[str] = []
    if active and active.get("conclusion") in _FAIL_SET:
        aid = int(active["databaseId"])
        diag = _download_artifacts(ctx, aid, lambda n: n == "deploy-gke-diagnostics")
    base_sha, e2e_sha = (green or {}).get("headSha", ""), target.get("headSha", "")
    commits, cluster_files = [], []
    if base_sha and e2e_sha and base_sha != e2e_sha:
        rng = f"{base_sha}..{e2e_sha}"
        log_r = _quiet(["git", "log", "--oneline", rng], ctx.repo_dir)
        diff_r = _quiet(["git", "diff", "--name-only", rng], ctx.repo_dir)
        if log_r.returncode == 0 and diff_r.returncode == 0:
            commits = [ln for ln in log_r.stdout.splitlines() if ln.strip()]
            cluster_files = [f for f in diff_r.stdout.splitlines() if f.startswith(_CLUSTER_DIRS)]
    skew = {"active_build_run": active, "last_green_build_run": green}
    return skew | {"undeployed_commits": commits, "cluster_deployed_files": cluster_files}, diag


def _report(status: str, latest: Any, target: Any, issues: Any, warnings: list[str], **kw: Any):
    d = {"status_summary": status, "is_latest_e2e_failing": False, "target_run": target}
    d |= {
        "latest_completed_e2e_run": latest,
        "superseded_by_green_run": None,
        "open_issues": issues,
    }
    d |= {"failed_jobs": [], "downloaded_artifacts": [], "rollout_skew": None, "scratch_dir": None}
    return d | kw | {"warnings": warnings}


def triage_nightly_run(args: argparse.Namespace) -> dict[str, Any]:
    """Executes Stage 1 & Stage 2 nightly discovery, artifact collection, and skew analysis."""
    repo, branch, repo_dir = _validate_inputs(args)
    warnings: list[str] = []
    fetch_res = _quiet(["git", "fetch", "--", "origin", branch], repo_dir)
    if fetch_res.returncode != 0:
        warnings.append(f"git fetch origin {branch} failed: {fetch_res.stderr.strip()}")
    e2e_q = f"run list --repo={repo} --workflow=nightly-examples-e2e.yml --branch={branch} --limit={args.limit} --json={_E2E_JSON}"
    e2e_runs = _gh_json(e2e_q.split(), repo_dir) or []
    iss_q = f"issue list --repo={repo} --label=nightly-failure --state=open --json=number,title,url,createdAt"
    issues = _gh_json(iss_q.split(), repo_dir, warnings) or []
    latest, target = _select_target_run(repo, args.run_id, e2e_runs, repo_dir)
    if target is None:
        return _report("healthy", latest, None, issues, warnings)
    run_id = int(target["databaseId"])
    default_dir = Path(os.environ.get("ARTIFACT_DIR", "/tmp")) / f"nightly_triage_{run_id}"
    scratch = Path(args.scratch_dir) if args.scratch_dir else default_dir
    scratch.mkdir(parents=True, exist_ok=True)
    ctx = _TriageCtx(repo, repo_dir, scratch, warnings)
    nmap = _load_matrix_name_map(getattr(args, "repo_dir", None))
    jobs, failed_ids = _collect_failed_jobs(ctx, run_id, str(target.get("conclusion", "")), nmap)
    wanted = {f"result-{eid}" for eid in failed_ids}
    is_wanted = lambda n: n.startswith("ui-screenshots-") or n in wanted  # noqa: E731
    arts = _download_artifacts(ctx, run_id, is_wanted)
    bld_q = f"run list --repo={repo} --workflow=nightly-build.yml --branch={branch} --limit=15 --json={_BUILD_JSON}"
    builds = _gh_json(bld_q.split(), repo_dir, warnings) or []
    skew, diag = _compute_rollout_skew(ctx, target, builds)
    l_ts = latest.get("createdAt", "") if latest and latest.get("conclusion") == "success" else ""
    sup = latest if l_ts > target.get("createdAt", "") else None
    is_fail = bool(latest and latest.get("conclusion") not in _OK_SET)
    extra: dict[str, Any] = {"is_latest_e2e_failing": is_fail, "superseded_by_green_run": sup}
    extra |= {"failed_jobs": jobs, "downloaded_artifacts": [*arts, *diag], "rollout_skew": skew}
    return _report("failing", latest, target, issues, warnings, **extra, scratch_dir=str(scratch))


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
