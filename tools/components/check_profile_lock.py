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

"""Enforces GA release locks on component profiles under deploy/profiles/."""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_command, run_tool  # noqa: E402

LOCKED_PROFILES_REL_PATH = "deploy/profiles/locked_profiles.yaml"
PROFILES_DIR_PREFIX = "deploy/profiles/"
OVERRIDE_TRAILER_KEY = "PROFILE-LOCK-OVERRIDE"
OVERRIDE_ENV_VAR = "PROFILE_LOCK_OVERRIDE"
_ZERO_SHA = "0" * 40
_ALLOWED_PROFILE_KEYS = frozenset({"locked_since", "chart_digest", "embedded_charts_digest"})
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_TRAILER_RE = re.compile(
    rf"^\s*{re.escape(OVERRIDE_TRAILER_KEY)}\s*:\s*(\S+)(?:\s+(.*\S))?\s*$",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class LockedProfile:
    """Metadata for a GA-locked component profile in locked_profiles.yaml."""

    name: str
    locked_since: str
    chart_digest: str = ""
    embedded_charts_digest: str = ""


@dataclass(frozen=True)
class LockViolation:
    """Represents a locked profile modified without a valid override trailer."""

    profile: str
    locked_since: str
    files: tuple[str, ...]


def _strip_comment_outside_quotes(raw: str) -> str:
    """Strips a trailing `#` comment outside single or double quotes."""
    in_quote: str | None = None
    for idx, ch in enumerate(raw):
        if ch in ('"', "'"):
            if in_quote is None:
                in_quote = ch
            elif in_quote == ch:
                in_quote = None
        elif ch == "#" and in_quote is None:
            return raw[:idx]
    return raw


def _unquote_yaml_token(raw: str) -> str:
    """Strips whitespace and matching surrounding quotes from a YAML key or scalar."""
    val = raw.strip()
    if len(val) >= 2 and val[0] in ('"', "'") and val[-1] == val[0]:
        return val[1:-1].strip()
    return val


def _strip_yaml_comment_and_quotes(raw: str) -> str:
    """Strips inline comments and surrounding quotes from a scalar YAML value."""
    return _unquote_yaml_token(_strip_comment_outside_quotes(raw))


def _split_inline_items(inner: str) -> list[str]:
    """Splits comma-separated flow-mapping items while respecting quotes."""
    items: list[str] = []
    start = 0
    in_quote: str | None = None
    for idx, ch in enumerate(inner):
        if ch in ('"', "'"):
            if in_quote is None:
                in_quote = ch
            elif in_quote == ch:
                in_quote = None
        elif ch == "," and in_quote is None:
            items.append(inner[start:idx])
            start = idx + 1
    items.append(inner[start:])
    return [item.strip() for item in items if item.strip()]


def _parse_inline_mapping(profile_name: str, raw_body: str) -> dict[str, str]:
    """Parses a single-line flow mapping like `{locked_since: "1.0.0", ...}`."""
    cleaned = _strip_comment_outside_quotes(raw_body).strip()
    inner = cleaned[1:-1].strip()
    if not inner:
        return {}
    fields: dict[str, str] = {}
    for item in _split_inline_items(inner):
        if ":" not in item:
            raise HandledError(
                f"Malformed inline mapping for profile '{profile_name}' in {LOCKED_PROFILES_REL_PATH}.",
                instruction=f"Use 'key: value' entries under '{profile_name}' in {LOCKED_PROFILES_REL_PATH}.",
            )
        raw_k, raw_v = item.split(":", 1)
        k = _unquote_yaml_token(raw_k)
        if not k or k in fields:
            raise HandledError(
                f"Invalid or duplicate key '{k}' for profile '{profile_name}' in {LOCKED_PROFILES_REL_PATH}.",
                instruction=f"Specify each key once under '{profile_name}' in {LOCKED_PROFILES_REL_PATH}.",
            )
        fields[k] = _strip_yaml_comment_and_quotes(raw_v)
    return fields


def _build_locked_profile(name: str, fields: Mapping[str, str]) -> LockedProfile:
    """Constructs and validates a LockedProfile from parsed YAML key/value pairs."""
    unknown_keys = sorted(set(fields) - _ALLOWED_PROFILE_KEYS)
    if unknown_keys:
        raise HandledError(
            f"Locked profile '{name}' in {LOCKED_PROFILES_REL_PATH} has unknown key(s): {', '.join(unknown_keys)}.",
            instruction=f"Only {sorted(_ALLOWED_PROFILE_KEYS)} are permitted in {LOCKED_PROFILES_REL_PATH}.",
        )
    locked_since = fields.get("locked_since", "").strip()
    if not locked_since:
        raise HandledError(
            f"Locked profile '{name}' in {LOCKED_PROFILES_REL_PATH} is missing 'locked_since'.",
            instruction=f"Set 'locked_since: \"<xmc-version>\"' for '{name}' in {LOCKED_PROFILES_REL_PATH}.",
        )
    chart_digest = fields.get("chart_digest", "").strip()
    embedded_digest = fields.get("embedded_charts_digest", "").strip()
    for field_name, digest_val in (
        ("chart_digest", chart_digest),
        ("embedded_charts_digest", embedded_digest),
    ):
        if not _SHA256_RE.match(digest_val):
            raise HandledError(
                f"Locked profile '{name}' in {LOCKED_PROFILES_REL_PATH} has missing or invalid '{field_name}'.",
                instruction=f"Set '{field_name}' for '{name}' to a 64-character lowercase hex SHA-256 digest.",
            )
    return LockedProfile(
        name=name,
        locked_since=locked_since,
        chart_digest=chart_digest,
        embedded_charts_digest=embedded_digest,
    )


def parse_locked_profiles_yaml(content: str) -> dict[str, LockedProfile]:
    """Parses deploy/profiles/locked_profiles.yaml into LockedProfile records."""
    raw_entries: dict[str, dict[str, str]] = {}
    current_profile: str | None = None

    for lineno, raw_line in enumerate(content.splitlines(), start=1):
        stripped = _strip_comment_outside_quotes(raw_line).strip()
        if not stripped or stripped == "---":
            continue
        indent = len(raw_line) - len(raw_line.lstrip(" "))
        if ":" not in stripped:
            raise HandledError(
                f"Invalid line {lineno} in {LOCKED_PROFILES_REL_PATH}: '{raw_line.strip()}'.",
                instruction=f"Ensure {LOCKED_PROFILES_REL_PATH} uses valid YAML key-value syntax.",
            )
        key_part, val_part = stripped.split(":", 1)
        key = _unquote_yaml_token(key_part)
        val_part = val_part.strip()

        if indent == 0:
            if not key or key in raw_entries:
                raise HandledError(
                    f"Invalid or duplicate profile key '{key}' at line {lineno} of {LOCKED_PROFILES_REL_PATH}.",
                    instruction=f"Define each top-level profile key once in {LOCKED_PROFILES_REL_PATH}.",
                )
            if val_part:
                if not (val_part.startswith("{") and val_part.endswith("}")):
                    raise HandledError(
                        f"Unexpected scalar value for profile '{key}' at line {lineno} of {LOCKED_PROFILES_REL_PATH}.",
                        instruction=f"Use a mapping under '{key}' in {LOCKED_PROFILES_REL_PATH}.",
                    )
                raw_entries[key] = _parse_inline_mapping(key, val_part)
                current_profile = None
            else:
                raw_entries[key] = {}
                current_profile = key
        else:
            if current_profile is None:
                raise HandledError(
                    f"Unexpected indentation at line {lineno} in {LOCKED_PROFILES_REL_PATH}.",
                    instruction=f"Nest profile attributes under a top-level profile in {LOCKED_PROFILES_REL_PATH}.",
                )
            if not key or key in raw_entries[current_profile]:
                raise HandledError(
                    f"Invalid or duplicate key '{key}' under '{current_profile}' at line {lineno} of {LOCKED_PROFILES_REL_PATH}.",
                    instruction=f"Specify each attribute once under '{current_profile}'.",
                )
            raw_entries[current_profile][key] = _strip_yaml_comment_and_quotes(val_part)

    return {name: _build_locked_profile(name, fields) for name, fields in raw_entries.items()}


def _git_rev_exists(repo_root: Path, rev: str) -> bool:
    """Returns True if `<rev>^{commit}` resolves in `repo_root`."""
    proc = subprocess.run(
        ["git", "rev-parse", "--verify", "--quiet", f"{rev}^{{commit}}"],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=False,
    )
    return proc.returncode == 0


def _read_git_file_if_present(repo_root: Path, rev: str, rel_path: str) -> str | None:
    """Returns file contents at `<rev>:<rel_path>`, or None if rev or path is absent."""
    if rev == "":
        ls_idx = run_command(
            ["git", "ls-files", "--stage", "--", rel_path],
            cwd=repo_root,
            capture_output=True,
            check=True,
            log_cmd=False,
            log_on_error=False,
            handled_instruction="Ensure git is available and run from a valid repository checkout.",
        )
        if not ls_idx.stdout.strip():
            return None
        return run_command(
            ["git", "show", f":{rel_path}"],
            cwd=repo_root,
            capture_output=True,
            check=True,
            log_cmd=False,
            log_on_error=False,
            handled_instruction=f"Failed to read staged '{rel_path}' from the git index.",
        ).stdout

    if not _git_rev_exists(repo_root, rev):
        return None
    ls_tree = run_command(
        ["git", "ls-tree", "--", rev, rel_path],
        cwd=repo_root,
        capture_output=True,
        check=True,
        log_cmd=False,
        log_on_error=False,
        handled_instruction=f"Failed to inspect '{rel_path}' at '{rev}'.",
    )
    if not ls_tree.stdout.strip():
        return None
    return run_command(
        ["git", "show", f"{rev}:{rel_path}"],
        cwd=repo_root,
        capture_output=True,
        check=True,
        log_cmd=False,
        log_on_error=False,
        handled_instruction=f"Failed to read '{rel_path}' at '{rev}'.",
    ).stdout


def _read_active_lock_registry_text(repo_root: Path, *, staged: bool, head_ref: str) -> str:
    """Reads the active locked_profiles.yaml content for staged or branch-diff mode."""
    lock_file = repo_root / LOCKED_PROFILES_REL_PATH
    if staged:
        staged_content = _read_git_file_if_present(repo_root, "", LOCKED_PROFILES_REL_PATH)
        if staged_content is not None:
            return staged_content
        if not lock_file.is_file():
            raise HandledError(
                f"Locked profiles registry not found at {lock_file}.",
                instruction=f"Ensure {LOCKED_PROFILES_REL_PATH} exists in the repository.",
            )
        return lock_file.read_text(encoding="utf-8")

    if head_ref and head_ref != "HEAD":
        if not _git_rev_exists(repo_root, head_ref):
            raise HandledError(
                f"Invalid git head reference: '{head_ref}'.",
                instruction="Pass a valid commit or ref to --head-ref.",
            )
        head_content = _read_git_file_if_present(repo_root, head_ref, LOCKED_PROFILES_REL_PATH)
        if head_content is None:
            raise HandledError(
                f"Locked profiles registry not found at {head_ref}:{LOCKED_PROFILES_REL_PATH}.",
                instruction=f"Ensure {LOCKED_PROFILES_REL_PATH} exists at {head_ref}.",
            )
        return head_content

    if not lock_file.is_file():
        raise HandledError(
            f"Locked profiles registry not found at {lock_file}.",
            instruction=f"Ensure {LOCKED_PROFILES_REL_PATH} exists in the repository.",
        )
    return lock_file.read_text(encoding="utf-8")


def _verify_locked_profile_dirs(
    repo_root: Path,
    active_profiles: Mapping[str, LockedProfile],
    changed_files: Sequence[str] = (),
) -> None:
    """Verifies each locked profile maps to an existing deploy/profiles/<name>/ directory."""
    touched_profiles: set[str] = set()
    for raw_path in changed_files:
        norm = raw_path.replace("\\", "/").strip().lstrip("./")
        if norm.startswith(PROFILES_DIR_PREFIX):
            parts = [p for p in norm[len(PROFILES_DIR_PREFIX) :].split("/") if p]
            if len(parts) >= 2:
                touched_profiles.add(parts[0])
    for name in sorted(active_profiles):
        if name in touched_profiles:
            continue
        profile_dir = repo_root / PROFILES_DIR_PREFIX / name
        if not profile_dir.is_dir():
            raise HandledError(
                f"Locked profile '{name}' in {LOCKED_PROFILES_REL_PATH} has no matching directory at {PROFILES_DIR_PREFIX}{name}.",
                instruction=f"Ensure {PROFILES_DIR_PREFIX}{name}/ exists or fix the profile key in {LOCKED_PROFILES_REL_PATH}.",
            )


def _load_locked_profiles_with_mutations(
    repo_root: Path,
    base_ref: str | None = None,
    *,
    staged: bool = False,
    head_ref: str = "HEAD",
    changed_files: Sequence[str] = (),
) -> tuple[dict[str, LockedProfile], list[str]]:
    """Loads locked profiles, rejects removals, and returns profiles whose lock metadata changed."""
    active = parse_locked_profiles_yaml(
        _read_active_lock_registry_text(repo_root, staged=staged, head_ref=head_ref)
    )
    _verify_locked_profile_dirs(repo_root, active, changed_files)

    profiles = dict(active)
    mutated_in_registry: set[str] = set()
    baseline_refs: list[str] = []
    if staged:
        baseline_refs.append("HEAD")
    if base_ref and base_ref not in baseline_refs:
        baseline_refs.append(base_ref)

    for ref in baseline_refs:
        base_content = _read_git_file_if_present(repo_root, ref, LOCKED_PROFILES_REL_PATH)
        if not base_content:
            continue
        for name, base_entry in parse_locked_profiles_yaml(base_content).items():
            if name not in active:
                raise HandledError(
                    f"Cannot remove GA-locked profile '{name}' (locked since XMC {base_entry.locked_since}) from {LOCKED_PROFILES_REL_PATH}.",
                    instruction=f"Keep '{name}' in {LOCKED_PROFILES_REL_PATH}; GA-locked profiles cannot be unlocked.",
                )
            if active[name] != base_entry:
                mutated_in_registry.add(name)
                profiles[name] = base_entry
    return profiles, sorted(mutated_in_registry)


def load_locked_profiles(
    repo_root: Path,
    base_ref: str | None = None,
    *,
    staged: bool = False,
    head_ref: str = "HEAD",
) -> dict[str, LockedProfile]:
    """Loads locked profiles and validates against baseline refs and profile directories."""
    profiles, _ = _load_locked_profiles_with_mutations(
        repo_root,
        base_ref=base_ref,
        staged=staged,
        head_ref=head_ref,
    )
    return profiles


def classify_locked_profile_changes(
    changed_files: Sequence[str],
    locked_profiles: Mapping[str, LockedProfile],
    mutated_registry_profiles: Sequence[str] = (),
) -> dict[str, list[str]]:
    """Groups changed file paths under any locked profile directory by profile name."""
    touched: dict[str, set[str]] = {}
    for raw_path in changed_files:
        norm = raw_path.replace("\\", "/").strip().lstrip("./")
        if not norm.startswith(PROFILES_DIR_PREFIX):
            continue
        parts = [p for p in norm[len(PROFILES_DIR_PREFIX) :].split("/") if p]
        if len(parts) < 2:
            continue
        profile_name = parts[0]
        if profile_name in locked_profiles:
            touched.setdefault(profile_name, set()).add(norm)

    for profile_name in mutated_registry_profiles:
        if profile_name in locked_profiles:
            touched.setdefault(profile_name, set()).add(LOCKED_PROFILES_REL_PATH)

    return {prof: sorted(paths) for prof, paths in sorted(touched.items())}


def parse_override_trailers(messages: Sequence[str]) -> dict[str, str]:
    """Extracts valid `PROFILE-LOCK-OVERRIDE: <profile> <reason>` trailers from commit messages."""
    overrides: dict[str, str] = {}
    for msg in messages:
        for line in msg.splitlines():
            match = _TRAILER_RE.match(line)
            if not match:
                continue
            profile_name = match.group(1).strip()
            reason = (match.group(2) or "").strip()
            if profile_name and reason:
                overrides[profile_name] = reason
    return overrides


def get_staged_files(repo_root: Path) -> list[str]:
    """Returns staged file paths (additions, modifications, deletions, and renames)."""
    res = run_command(
        [
            "git",
            "-c",
            "core.quotePath=false",
            "diff",
            "--cached",
            "--name-only",
            "--no-renames",
            "-z",
        ],
        cwd=repo_root,
        capture_output=True,
        check=True,
        log_cmd=False,
        log_on_error=False,
        handled_instruction="Ensure git is available and run from a valid repository checkout.",
    )
    staged = {entry for entry in res.stdout.split("\0") if entry}
    if staged and _git_rev_exists(repo_root, "MERGE_HEAD"):
        vs_merge = run_command(
            [
                "git",
                "-c",
                "core.quotePath=false",
                "diff",
                "--cached",
                "--name-only",
                "--no-renames",
                "-z",
                "MERGE_HEAD",
            ],
            cwd=repo_root,
            capture_output=True,
            check=True,
            log_cmd=False,
            log_on_error=False,
            handled_instruction="Failed to compare staged index against MERGE_HEAD.",
        )
        staged &= {entry for entry in vs_merge.stdout.split("\0") if entry}
    return sorted(staged)


def _resolve_effective_base_ref(repo_root: Path, base_ref: str | None) -> str:
    """Resolves a usable git base reference for branch diff comparisons."""
    candidate = (base_ref or "").strip()
    if not candidate or candidate == _ZERO_SHA:
        gh_base = os.environ.get("GITHUB_BASE_REF", "").strip()
        candidate = f"origin/{gh_base}" if gh_base else "origin/main"
    if not _git_rev_exists(repo_root, candidate) and not candidate.startswith("origin/"):
        origin_cand = f"origin/{candidate}"
        if _git_rev_exists(repo_root, origin_cand):
            return origin_cand
    return candidate


def get_branch_diff_files(repo_root: Path, base_ref: str, head_ref: str = "HEAD") -> list[str]:
    """Returns file paths changed between the merge-base of base_ref and head_ref."""
    res = run_command(
        [
            "git",
            "-c",
            "core.quotePath=false",
            "diff",
            "--name-only",
            "--no-renames",
            "-z",
            f"{base_ref}...{head_ref}",
        ],
        cwd=repo_root,
        capture_output=True,
        check=True,
        log_cmd=False,
        log_on_error=False,
        handled_instruction=f"Fetch the base branch first (e.g. 'git fetch origin') so '{base_ref}' resolves.",
    )
    return sorted({entry for entry in res.stdout.split("\0") if entry})


def get_commit_messages(
    repo_root: Path,
    base_ref: str | None,
    head_ref: str = "HEAD",
    *,
    staged: bool = False,
) -> list[str]:
    """Collects commit messages (and staged env override if applicable) for trailer inspection."""
    messages: list[str] = []
    if staged:
        env_override = os.environ.get(OVERRIDE_ENV_VAR, "").strip()
        if env_override:
            if env_override.upper().startswith(f"{OVERRIDE_TRAILER_KEY}:"):
                messages.append(env_override)
            else:
                messages.append(f"{OVERRIDE_TRAILER_KEY}: {env_override}")

    if base_ref:
        proc = subprocess.run(
            ["git", "log", "--format=%B", "-z", f"{base_ref}..{head_ref}"],
            cwd=repo_root,
            capture_output=True,
            text=True,
            check=False,
        )
        if proc.returncode == 0 and proc.stdout.strip():
            messages.extend(m for m in proc.stdout.split("\0") if m.strip())
            return messages

    if not staged:
        head_proc = subprocess.run(
            ["git", "log", "-1", "--format=%B", head_ref],
            cwd=repo_root,
            capture_output=True,
            text=True,
            check=False,
        )
        if head_proc.returncode == 0 and head_proc.stdout.strip():
            messages.append(head_proc.stdout)
    return messages


def evaluate_profile_locks(
    changed_files: Sequence[str],
    locked_profiles: Mapping[str, LockedProfile],
    commit_messages: Sequence[str],
    mutated_registry_profiles: Sequence[str] = (),
) -> tuple[list[LockViolation], dict[str, str]]:
    """Evaluates changed files against locked profiles and override trailers."""
    touched = classify_locked_profile_changes(
        changed_files,
        locked_profiles,
        mutated_registry_profiles=mutated_registry_profiles,
    )
    if not touched:
        return [], {}

    overrides = parse_override_trailers(commit_messages)
    violations: list[LockViolation] = []
    applied_overrides: dict[str, str] = {}

    for profile_name, files in touched.items():
        if profile_name in overrides:
            applied_overrides[profile_name] = overrides[profile_name]
        else:
            meta = locked_profiles[profile_name]
            violations.append(
                LockViolation(
                    profile=profile_name,
                    locked_since=meta.locked_since,
                    files=tuple(files),
                )
            )
    return violations, applied_overrides


def _format_violation_error(violations: Sequence[LockViolation]) -> HandledError:
    """Formats a clear, actionable HandledError describing lock violations and override steps."""
    lines: list[str] = []
    override_examples: list[str] = []
    for v in violations:
        file_list = ", ".join(v.files)
        lines.append(
            f"Profile '{v.profile}' is GA-locked since XMC {v.locked_since}; "
            f"offending file(s): {file_list}"
        )
        override_examples.append(f"{OVERRIDE_TRAILER_KEY}: {v.profile} <patch release reason>")

    message = "GA-locked component profile modification blocked. " + " | ".join(lines)
    instruction = (
        "New features, component additions, or breaking changes must target a new component "
        "profile (e.g., deploy/profiles/stable-2026b/) instead of editing a GA-locked profile. "
        "For a legitimate patch release to a locked profile, include the commit trailer "
        f"`{'` and `'.join(override_examples)}` in your commit message, update the pinned "
        f"digests in `{LOCKED_PROFILES_REL_PATH}`, and obtain CODEOWNERS approval. "
        f'(For local `.githooks/pre-commit`, pass `{OVERRIDE_ENV_VAR}="<profile> <reason>" git commit ...`.)'
    )
    details = {
        "locked_profiles_file": LOCKED_PROFILES_REL_PATH,
        "override_trailer_format": f"{OVERRIDE_TRAILER_KEY}: <profile> <reason>",
        "violations": [
            {
                "profile": v.profile,
                "locked_since": v.locked_since,
                "files": list(v.files),
            }
            for v in violations
        ],
    }
    return HandledError(message, instruction=instruction, details=details)


def _validate_repo_root(raw_repo_root: str) -> Path:
    """Validates and resolves the git repository root directory."""
    repo_root = Path(raw_repo_root).resolve()
    if not repo_root.is_dir() or not (repo_root / ".git").exists():
        raise HandledError(
            f"Invalid git repository root: {repo_root}",
            instruction="Pass --repo-root pointing to a valid git repository checkout.",
        )
    return repo_root


def check_profile_lock(args: argparse.Namespace) -> dict[str, Any]:
    """Main execution handler for checking component profile locks."""
    repo_root = _validate_repo_root(args.repo_root)
    effective_base = _resolve_effective_base_ref(repo_root, args.base_ref)
    changed_files = (
        get_staged_files(repo_root)
        if args.staged
        else get_branch_diff_files(repo_root, effective_base, args.head_ref)
    )
    locked_profiles, mutated_profiles = _load_locked_profiles_with_mutations(
        repo_root,
        base_ref=effective_base,
        staged=args.staged,
        head_ref=args.head_ref,
        changed_files=changed_files,
    )
    commit_messages = get_commit_messages(
        repo_root, effective_base, args.head_ref, staged=args.staged
    )
    violations, applied_overrides = evaluate_profile_locks(
        changed_files, locked_profiles, commit_messages, mutated_profiles
    )
    if violations:
        raise _format_violation_error(violations)
    return {
        "mode": "staged" if args.staged else "branch-diff",
        "base_ref": effective_base,
        "locked_profiles": sorted(locked_profiles.keys()),
        "changed_files_count": len(changed_files),
        "overrides": applied_overrides,
    }


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser for check_profile_lock.py."""
    parser = argparse.ArgumentParser(
        description=(
            "Fail when a diff modifies files under a GA-locked component profile in "
            f"{LOCKED_PROFILES_REL_PATH} without a '{OVERRIDE_TRAILER_KEY}: <profile> <reason>' "
            "commit trailer."
        )
    )
    parser.add_argument(
        "--repo-root",
        default=str(_REPO_ROOT),
        help="Repository root directory.",
    )
    parser.add_argument(
        "--staged",
        action="store_true",
        help="Check staged files in the git index (used by .githooks/pre-commit).",
    )
    parser.add_argument(
        "--base-ref",
        default="",
        help="Git base reference for branch diffs (default: $GITHUB_BASE_REF or origin/main).",
    )
    parser.add_argument(
        "--head-ref",
        default="HEAD",
        help="Git head reference for branch diffs (default: HEAD).",
    )
    return parser


def main() -> None:
    """CLI entrypoint."""
    run_tool(build_parser(), check_profile_lock)


if __name__ == "__main__":
    main()
