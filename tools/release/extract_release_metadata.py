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

"""Extracts release metadata, configuration diffs, flag transitions, and PR scopes."""

import argparse
import json
import re
import subprocess
import sys
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError, run_tool
except ImportError:
    from common.tool_utils import HandledError, run_tool  # type: ignore[no-redef]


@dataclass(kw_only=True)
class CommitInfo:
    """Represents metadata for a single git commit in the release range."""

    commit_hash: str
    short_hash: str
    subject: str
    author: str
    date: str
    commit_type: str = "other"
    scope: str = ""
    description: str = ""
    pr_number: int | None = None
    files_changed: list[str] = field(default_factory=list)
    category: str = "developer_velocity"
    gated_flag: str | None = None
    gated_subsystem: str | None = None
    is_promotion: bool = False


@dataclass(kw_only=True)
class FlagTransition:
    """Represents a feature flag transition between base and target refs."""

    flag_name: str
    source_file: str
    base_value: bool | str | None
    target_value: bool | str | None
    is_flip: bool
    is_promotion_to_default: bool
    description: str


@dataclass(kw_only=True)
class VersionRecommendation:
    """Represents the recommended semantic version increment and rationale."""

    current_base_version: str
    recommended_version: str
    recommended_bump: str  # 'major', 'minor', 'patch'
    has_breaking_changes: bool
    has_promotions: bool
    has_ootb_features: bool
    has_ootb_fixes: bool
    has_approved_features: bool = False
    has_migrations: bool = False
    rationale: str
    bump_triggers: list[str] = field(default_factory=list)


@dataclass(kw_only=True)
class FeatureFlagInfo:
    """Represents a dynamically discovered feature flag in deployment configurations."""

    flag_name: str
    display_name: str
    tokens: set[str] = field(default_factory=set)
    ctk_value: bool | None = None
    helm_value: bool | None = None
    is_active: bool = False


def normalize_flag_name(flag: str) -> str:
    """Strips common prefix/suffix boilerplate to extract the core subsystem identifier."""
    clean = flag
    if clean.startswith("vars."):
        clean = clean[5:]
    if clean.startswith("enable_"):
        clean = clean[7:]
    elif clean.startswith("use_"):
        clean = clean[4:]
    if clean.endswith("_deployment_enabled"):
        clean = clean[:-19]
    elif clean.endswith("Deployment.enabled"):
        clean = clean[:-18]
    elif clean.endswith("_enabled") or clean.endswith(".enabled"):
        clean = clean[:-8]
    return clean.lower()


def derive_flag_tokens(flag_name: str) -> set[str]:
    """Generates matching tokens and search terms for a given feature flag name."""
    core = normalize_flag_name(flag_name)
    tokens = {core, core.replace("_", "-"), core.replace("-", "_")}
    if core.endswith("s"):
        tokens.add(core[:-1])
    else:
        tokens.add(f"{core}s")

    # Add domain-level aliases
    if "rod" in core:
        tokens.update(["rod", "rod_data_provider", "rod-data-provider", "rod-ui", "rod-backend"])
    if "health" in core:
        tokens.update(["health", "health_service", "health-service", "admin_panel", "admin-panel"])
    if "dashboard" in core or "dash" in core:
        tokens.update(
            ["dashboard", "dashboards", "xdash", "dashboard_service", "dashboard-service"]
        )
    if "metric" in core or "ingestion" in core:
        tokens.update(["metrics", "metric", "ingestion", "ingestion_gateway", "metrics_ingestion"])
    if "component" in core:
        tokens.update(["component", "components", "cms"])

    return {t for t in tokens if t}


def derive_display_name(flag_name: str) -> str:
    """Derives a human-readable title from a flag name."""
    core = normalize_flag_name(flag_name)
    special_cases = {
        "rod": "Resource Observability Dashboard (ROD)",
        "cms": "Component Management Service (CMS)",
        "xdash": "Dashboard Service & xdash SDK",
        "health_service": "Health Service & Admin Panel",
        "health": "Health Service & Admin Panel",
        "components": "Components Engine",
        "component": "Components Engine",
        "metrics": "Metrics & Ingestion Gateway",
        "metric": "Metrics & Ingestion Gateway",
        "ingestion": "Metrics & Ingestion Gateway",
    }
    if core in special_cases:
        return special_cases[core]

    words = [w.capitalize() for w in re.split(r"[_.-]", core) if w]
    return " ".join(words)


def parse_conventional_commit(subject: str) -> tuple[str, str, str, int | None]:
    """Parses conventional commit headers: type(scope): description (#123)."""
    pr_number: int | None = None
    pr_match = re.search(r"\(#(\d+)\)\s*$", subject)
    if pr_match:
        pr_number = int(pr_match.group(1))
        clean_subject = subject[: pr_match.start()].strip()
    else:
        clean_subject = subject.strip()

    conv_match = re.match(r"^([a-zA-Z]+)(?:\(([^)]+)\))?!?:\s*(.*)$", clean_subject)
    if conv_match:
        commit_type = conv_match.group(1).lower()
        scope = conv_match.group(2).lower() if conv_match.group(2) else ""
        desc = conv_match.group(3).strip()
        return commit_type, scope, desc, pr_number

    return "other", "", clean_subject, pr_number


def parse_yaml_booleans(yaml_content: str) -> dict[str, bool]:
    """Extracts boolean flags from YAML content, supporting nested keys."""
    try:
        import yaml

        data = yaml.safe_load(yaml_content)
        if isinstance(data, dict):

            def flatten(d: dict[str, Any], prefix: str = "") -> dict[str, bool]:
                flat: dict[str, bool] = {}
                for k, v in d.items():
                    key = f"{prefix}.{k}" if prefix else str(k)
                    if isinstance(v, dict):
                        flat.update(flatten(v, key))
                    elif isinstance(v, bool):
                        if key.startswith("vars."):
                            key = key[5:]
                        flat[key] = v
                return flat

            return flatten(data)
    except Exception:
        pass

    flags: dict[str, bool] = {}
    stack: list[tuple[int, str]] = []
    for line in yaml_content.splitlines():
        if not line.strip() or line.strip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        trimmed = line.strip()
        match = re.match(r"^([a-zA-Z0-9_.-]+):\s*(true|false)\b", trimmed, re.IGNORECASE)
        if match:
            key = match.group(1)
            val = match.group(2).lower() == "true"
            while stack and stack[-1][0] >= indent:
                stack.pop()
            full_key = ".".join([p[1] for p in stack] + [key])
            if full_key.startswith("vars."):
                full_key = full_key[5:]
            flags[full_key] = val
        else:
            parent_match = re.match(r"^([a-zA-Z0-9_.-]+):", trimmed)
            if parent_match:
                key = parent_match.group(1)
                while stack and stack[-1][0] >= indent:
                    stack.pop()
                stack.append((indent, key))
    return flags


def get_git_file_content(ref: str, file_path: str, repo_dir: Path) -> str:
    """Reads a file at a specific git ref."""
    try:
        res = subprocess.run(
            ["git", "show", f"{ref}:{file_path}"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout
    except subprocess.CalledProcessError:
        return ""


def get_latest_tag_before(target_ref: str, repo_dir: Path) -> str | None:
    """Finds the previous tag before the target reference."""
    try:
        res = subprocess.run(
            ["git", "describe", "--tags", "--abbrev=0", f"{target_ref}^"],
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
        return res.stdout.strip()
    except subprocess.CalledProcessError:
        try:
            res = subprocess.run(
                ["git", "tag", "--sort=-creatordate"],
                cwd=str(repo_dir),
                capture_output=True,
                text=True,
                check=True,
            )
            tags = [t.strip() for t in res.stdout.splitlines() if t.strip()]
            if target_ref in tags:
                idx = tags.index(target_ref)
                if idx + 1 < len(tags):
                    return tags[idx + 1]
            return tags[0] if tags else None
        except subprocess.CalledProcessError:
            return None


def extract_flag_transitions(
    base_ref: str, target_ref: str, repo_dir: Path
) -> list[FlagTransition]:
    """Compares CTK and Helm configuration files between base and target refs."""
    config_files = [
        "deploy/ctk_v2/xmanager-cloud-deployment.yaml",
        "deploy/ctk_v2/xmanager-cloud.yaml",
        "deploy/ctk_v2/xmc-deployment-template.yaml",
        "deploy/helm/values.yaml",
    ]
    transitions: list[FlagTransition] = []
    seen_flags: set[str] = set()

    for rel_path in config_files:
        base_content = get_git_file_content(base_ref, rel_path, repo_dir)
        target_content = get_git_file_content(target_ref, rel_path, repo_dir)

        if not base_content and not target_content:
            continue

        base_flags = parse_yaml_booleans(base_content)
        target_flags = parse_yaml_booleans(target_content)

        all_keys = set(base_flags.keys()).union(target_flags.keys())
        for key in sorted(all_keys):
            if key in seen_flags:
                continue

            base_val = base_flags.get(key)
            target_val = target_flags.get(key)

            if base_val != target_val:
                seen_flags.add(key)
                is_flip = (
                    isinstance(base_val, bool)
                    and isinstance(target_val, bool)
                    and base_val != target_val
                )
                is_promotion = base_val is False and target_val is True
                desc = (
                    f"Flag '{key}' flipped from {base_val} to {target_val}"
                    if is_flip
                    else f"Flag '{key}' modified ({base_val} -> {target_val})"
                )
                transitions.append(
                    FlagTransition(
                        flag_name=key,
                        source_file=rel_path,
                        base_value=base_val,
                        target_value=target_val,
                        is_flip=is_flip,
                        is_promotion_to_default=is_promotion,
                        description=desc,
                    )
                )

    return transitions


def discover_feature_flags(target_ref: str, repo_dir: Path) -> dict[str, FeatureFlagInfo]:
    """Dynamically extracts all feature flags from CTK and Helm configs at target ref."""
    ctk_candidate_files = [
        "deploy/ctk_v2/xmanager-cloud-deployment.yaml",
        "deploy/ctk_v2/xmanager-cloud.yaml",
        "deploy/ctk_v2/xmc-deployment-template.yaml",
    ]
    ctk_flags: dict[str, bool] = {}
    for rel_path in ctk_candidate_files:
        content = get_git_file_content(target_ref, rel_path, repo_dir)
        if content:
            ctk_flags.update(parse_yaml_booleans(content))

    helm_content = get_git_file_content(target_ref, "deploy/helm/values.yaml", repo_dir)
    helm_flags = parse_yaml_booleans(helm_content) if helm_content else {}

    all_raw_flags = set(ctk_flags.keys()) | set(helm_flags.keys())
    feature_toggle_names = {
        f
        for f in all_raw_flags
        if f.startswith(("enable_", "use_"))
        or f.endswith(("_enabled", "_deployment_enabled", ".enabled", "Deployment.enabled"))
    }

    discovered: dict[str, FeatureFlagInfo] = {}
    for flag in feature_toggle_names:
        core = normalize_flag_name(flag)
        ctk_val = ctk_flags.get(flag)
        helm_val = helm_flags.get(flag)

        if ctk_val is None:
            for k, v in ctk_flags.items():
                if normalize_flag_name(k) == core:
                    ctk_val = v
                    break
        if helm_val is None:
            for k, v in helm_flags.items():
                if normalize_flag_name(k) == core:
                    helm_val = v
                    break

        is_active = bool(ctk_val) or bool(helm_val)
        tokens = derive_flag_tokens(flag)
        display_name = derive_display_name(flag)

        info = FeatureFlagInfo(
            flag_name=flag,
            display_name=display_name,
            tokens=tokens,
            ctk_value=ctk_val,
            helm_value=helm_val,
            is_active=is_active,
        )
        discovered[core] = info
        discovered[flag] = info

    return discovered


def get_current_feature_states(target_ref: str, repo_dir: Path) -> dict[str, bool]:
    """Extracts the active boolean status of dynamically discovered feature flags."""
    flags = discover_feature_flags(target_ref, repo_dir)
    return {k: info.is_active for k, info in flags.items()}


def parse_semver(version_str: str) -> tuple[int, int, int]:
    """Parses 'vX.Y.Z' or 'X.Y.Z' into (major, minor, patch) integers."""
    clean = version_str.lstrip("v").strip()
    match = re.match(r"^(\d+)\.(\d+)\.(\d+)", clean)
    if match:
        return int(match.group(1)), int(match.group(2)), int(match.group(3))
    return 0, 0, 0


def calculate_version_recommendation(
    base_ref: str,
    commits: list[CommitInfo],
    transitions: list[FlagTransition],
    categorized_commits: dict[str, list[dict[str, Any]]],
) -> VersionRecommendation:
    """Calculates the recommended SemVer increment (Major, Minor, or Patch) based on changes."""
    major, minor, patch = parse_semver(base_ref)
    has_v_prefix = base_ref.startswith("v")
    prefix = "v" if has_v_prefix else ""

    bump_triggers: list[str] = []

    # 1. Check for breaking changes
    has_breaking = False
    for c in commits:
        if (
            "!" in c.commit_type
            or "BREAKING CHANGE" in c.subject
            or "BREAKING CHANGE" in c.description
        ):
            has_breaking = True
            bump_triggers.append(f"Breaking commit ({c.short_hash}): {c.subject}")
            break

    # 2. Check for promotions (flags flipped from false to true)
    promotions = [t for t in transitions if t.is_promotion_to_default]
    has_promotions = len(promotions) > 0
    for p in promotions:
        bump_triggers.append(f"Feature flag promoted to default: {p.flag_name} (false -> true)")

    # 3. Check for active out-of-the-box features
    ootb_features = categorized_commits.get("user_facing_features", [])
    has_ootb_features = len(ootb_features) > 0
    if has_ootb_features:
        bump_triggers.append(
            f"Added {len(ootb_features)} user-facing feature(s) enabled out-of-the-box"
        )

    # 4. Check for production-ready approved preview features
    approved_features = categorized_commits.get("approved_preview_features", [])
    has_approved_features = len(approved_features) > 0
    if has_approved_features:
        bump_triggers.append(
            f"Added {len(approved_features)} production-ready feature(s) approved for enablement behind flags"
        )

    # 5. Check for new configuration flags introduced
    new_flags = [t for t in transitions if t.base_value is None]
    if new_flags:
        for nf in new_flags:
            bump_triggers.append(f"New configuration flag introduced: {nf.flag_name}")

    # 6. Check for Database Schema Migrations
    migration_commits = [
        c
        for c in commits
        if any(
            "migrations/" in f
            or f.startswith("internal/storage/spanner/migrations/")
            or f.startswith("deploy/spanner/")
            for f in c.files_changed
        )
    ]
    has_migrations = len(migration_commits) > 0
    if has_migrations:
        bump_triggers.append(
            f"Database schema migration detected ({len(migration_commits)} commit(s) touching migrations directory)"
        )

    # 7. Check for user-facing fixes
    ootb_fixes = categorized_commits.get("user_facing_fixes", [])
    has_ootb_fixes = len(ootb_fixes) > 0

    # Determine recommended bump level
    if major == 0:
        # Zero-major (pre-1.0) SemVer:
        # Features, promotions, migrations, approved flags, or breaking changes bump MINOR (0.5.0 -> 0.6.0)
        # Bug fixes, chores, refactors bump PATCH (0.5.0 -> 0.5.1)
        if (
            has_breaking
            or has_promotions
            or has_ootb_features
            or has_approved_features
            or new_flags
            or has_migrations
        ):
            recommended_bump = "minor"
            recommended_version = f"{prefix}0.{minor + 1}.0"
            trigger_summary = (
                "breaking changes"
                if has_breaking
                else (
                    "promoted feature flags"
                    if has_promotions
                    else (
                        "database schema migrations"
                        if has_migrations
                        else (
                            "approved production-ready flags"
                            if has_approved_features
                            else "new active features / config flags"
                        )
                    )
                )
            )
            rationale = (
                f"Minor version bump from {base_ref} to {recommended_version}: "
                f"Release includes {trigger_summary} in pre-1.0 zero-major versioning."
            )
        else:
            recommended_bump = "patch"
            recommended_version = f"{prefix}0.{minor}.{patch + 1}"
            rationale = (
                f"Patch version bump from {base_ref} to {recommended_version}: "
                "Contains only bug fixes, infrastructure updates, and developer velocity improvements "
                "with no new active out-of-the-box user features or migrations."
            )
    else:
        # Post-1.0 Standard SemVer:
        if has_breaking:
            recommended_bump = "major"
            recommended_version = f"{prefix}{major + 1}.0.0"
            rationale = (
                f"Major version bump from {base_ref} to {recommended_version}: "
                "Contains breaking / backwards-incompatible changes."
            )
        elif (
            has_promotions
            or has_ootb_features
            or has_approved_features
            or new_flags
            or has_migrations
        ):
            recommended_bump = "minor"
            recommended_version = f"{prefix}{major}.{minor + 1}.0"
            rationale = (
                f"Minor version bump from {base_ref} to {recommended_version}: "
                "Backwards-compatible new features, flag promotions, schema migrations, or configuration knobs."
            )
        else:
            recommended_bump = "patch"
            recommended_version = f"{prefix}{major}.{minor}.{patch + 1}"
            rationale = (
                f"Patch version bump from {base_ref} to {recommended_version}: "
                "Backwards-compatible bug fixes and internal maintenance only."
            )

    return VersionRecommendation(
        current_base_version=base_ref,
        recommended_version=recommended_version,
        recommended_bump=recommended_bump,
        has_breaking_changes=has_breaking,
        has_promotions=has_promotions,
        has_ootb_features=has_ootb_features,
        has_ootb_fixes=has_ootb_fixes,
        has_approved_features=has_approved_features,
        has_migrations=has_migrations,
        rationale=rationale,
        bump_triggers=bump_triggers,
    )


def classify_commit(
    commit: CommitInfo,
    active_states: dict[str, Any],
    promoted_flags: set[str],
    approved_flags: set[str] | None = None,
) -> None:
    """Classifies a commit into release categories based on files, type, and active flags."""
    # 1. Check if commit directly flips or introduces a promoted feature flag
    for flag in promoted_flags:
        if flag in commit.subject or flag in commit.description:
            commit.category = "highlights_and_promotions"
            commit.is_promotion = True
            return

    # 2. Explicit Tooling, CI, Linters, Rules, Tests, Documentation, Chores, Refactors
    if commit.commit_type in (
        "chore",
        "ci",
        "perf",
        "test",
        "docs",
        "refactor",
    ) or commit.scope in (
        "lint",
        "deps",
        "ci",
        "bazel",
        "rules",
        "absence",
        "test",
        "linting",
        "benchmark",
        "e2e",
    ):
        commit.category = "developer_velocity"
        return

    # 3. Match files changed or scope against dynamically discovered feature flags
    matched_flags: list[FeatureFlagInfo] = []
    flag_infos: list[FeatureFlagInfo] = []
    seen_flag_names: set[str] = set()

    for k, v in active_states.items():
        if isinstance(v, FeatureFlagInfo):
            if v.flag_name not in seen_flag_names:
                flag_infos.append(v)
                seen_flag_names.add(v.flag_name)
        else:
            flag_name = k
            if flag_name not in seen_flag_names:
                info = FeatureFlagInfo(
                    flag_name=flag_name,
                    display_name=derive_display_name(flag_name),
                    tokens=derive_flag_tokens(flag_name),
                    is_active=bool(v),
                )
                flag_infos.append(info)
                seen_flag_names.add(flag_name)

    for info in flag_infos:
        if commit.scope and any(commit.scope == tok or tok in commit.scope for tok in info.tokens):
            matched_flags.append(info)
            continue
        for f in commit.files_changed:
            parts = Path(f).parts
            if any(tok in parts or any(tok in part for part in parts) for tok in info.tokens):
                matched_flags.append(info)
                break

    if matched_flags:
        flag_info = matched_flags[0]
        commit.gated_subsystem = normalize_flag_name(flag_info.flag_name)
        commit.gated_flag = flag_info.flag_name

        if flag_info.is_active:
            if commit.commit_type == "fix":
                commit.category = "user_facing_fixes"
            else:
                commit.category = "user_facing_features"
        else:
            approved_set = approved_flags or set()
            normalized_approved = {normalize_flag_name(a) for a in approved_set}
            is_approved = (
                flag_info.flag_name in approved_set
                or normalize_flag_name(flag_info.flag_name) in normalized_approved
                or any(
                    tok in approved_set or tok in normalized_approved for tok in flag_info.tokens
                )
            )
            if is_approved:
                commit.category = "approved_preview_features"
            else:
                commit.category = "experimental_preview_features"
        return

    # 4. Pure Tooling, CI, Linters, Rules, Tests, Documentation files
    if commit.files_changed and all(
        f.startswith(".github/")
        or f.startswith(".agents/")
        or f.startswith("tools/")
        or f.startswith("scripts/")
        or f.startswith("docs/")
        or f.endswith("_test.go")
        or f.endswith("_test.py")
        or f in ("BUILD.bazel", "MODULE.bazel", "WORKSPACE", "ABSENCE.md")
        for f in commit.files_changed
    ):
        commit.category = "developer_velocity"
        return

    # 5. Infrastructure, Authentication & Deployment files
    if commit.scope in (
        "auth",
        "security",
        "infra",
        "infrastructure",
        "helm",
        "ctk",
        "terraform",
        "cloud",
        "dex",
    ) or any(
        f.startswith("deploy/")
        or f.startswith("terraform/")
        or f.startswith("pkg/auth/")
        or f.startswith("pkg/cloud/")
        or f.startswith("internal/gcemetadata/")
        or f.startswith(".githooks/")
        for f in commit.files_changed
    ):
        commit.category = "operational_and_infrastructure"
        return

    # 6. Fixes in core user-facing systems (UI, core backend, etc.)
    if commit.commit_type == "fix":
        commit.category = "user_facing_fixes"
        return

    # 7. Features in active core backend, UI, or API
    if commit.commit_type in ("feat", "other"):
        commit.category = "user_facing_features"
        return

    commit.category = "developer_velocity"


def get_git_commits(base_ref: str, target_ref: str, repo_dir: Path) -> list[CommitInfo]:
    """Retrieves and parses all git commits between base_ref and target_ref in a single git log invocation."""
    log_cmd = [
        "git",
        "log",
        "--pretty=format:COMMIT:%H%x09%h%x09%s%x09%an%x09%ad",
        "--date=short",
        "--name-only",
        f"{base_ref}..{target_ref}",
    ]
    try:
        log_res = subprocess.run(
            log_cmd,
            cwd=str(repo_dir),
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as e:
        raise HandledError(
            f"Git log failed for range '{base_ref}..{target_ref}': {e.stderr}",
            "Verify that both git references (tags/branches/hashes) exist in the repository.",
        )

    commits: list[CommitInfo] = []
    current_commit: CommitInfo | None = None
    current_files: list[str] = []

    for line in log_res.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        if line.startswith("COMMIT:"):
            if current_commit:
                current_commit.files_changed = current_files
                commits.append(current_commit)
                current_files = []
            parts = line[len("COMMIT:") :].split("\t")
            if len(parts) < 5:
                current_commit = None
                continue
            full_hash, short_hash, subject, author, commit_date = (
                parts[0],
                parts[1],
                parts[2],
                parts[3],
                parts[4],
            )
            commit_type, scope, desc, pr_number = parse_conventional_commit(subject)
            current_commit = CommitInfo(
                commit_hash=full_hash,
                short_hash=short_hash,
                subject=subject,
                author=author,
                date=commit_date,
                commit_type=commit_type,
                scope=scope,
                description=desc,
                pr_number=pr_number,
                files_changed=[],
            )
        elif current_commit:
            current_files.append(line)

    if current_commit:
        current_commit.files_changed = current_files
        commits.append(current_commit)

    return commits


def extract_release_metadata(args: argparse.Namespace) -> dict[str, Any]:
    """Primary execution handler for release metadata extraction."""
    repo_dir = Path(args.repo_dir).resolve()
    target_ref = args.target_ref

    base_ref = args.base_ref
    if not base_ref:
        base_ref = get_latest_tag_before(target_ref, repo_dir)
        if not base_ref:
            raise HandledError(
                f"Could not automatically determine previous tag for '{target_ref}'.",
                "Explicitly provide --base-ref (e.g. --base-ref 0.5.0).",
            )

    approved_flags = set(getattr(args, "approved_flags", None) or [])

    # 1. Extract flag transitions
    transitions = extract_flag_transitions(base_ref, target_ref, repo_dir)
    promoted_flags = {t.flag_name for t in transitions if t.is_promotion_to_default}

    # 2. Get active feature states at target ref
    discovered_flags = discover_feature_flags(target_ref, repo_dir)

    # 3. Extract commits and classify them
    commits = get_git_commits(base_ref, target_ref, repo_dir)
    for commit in commits:
        classify_commit(commit, discovered_flags, promoted_flags, approved_flags)

    # 4. Group commits by category
    categorized: dict[str, list[dict[str, Any]]] = {
        "highlights_and_promotions": [],
        "user_facing_features": [],
        "user_facing_fixes": [],
        "approved_preview_features": [],
        "experimental_preview_features": [],
        "operational_and_infrastructure": [],
        "developer_velocity": [],
    }

    for c in commits:
        cat_key = c.category if c.category in categorized else "developer_velocity"
        categorized[cat_key].append(asdict(c))

    # 5. Build Gated Subsystems Status Matrix dynamically
    gated_status_matrix = []
    seen_matrix_flags: set[str] = set()
    for flag_info in discovered_flags.values():
        if flag_info.flag_name in seen_matrix_flags:
            continue
        seen_matrix_flags.add(flag_info.flag_name)

        is_active = flag_info.is_active
        normalized_approved = {normalize_flag_name(a) for a in approved_flags}
        is_approved = (
            flag_info.flag_name in approved_flags
            or normalize_flag_name(flag_info.flag_name) in normalized_approved
            or any(tok in approved_flags or tok in normalized_approved for tok in flag_info.tokens)
        )
        if is_active:
            status = "Active (Out-of-the-Box)"
        elif is_approved:
            status = "Production-Ready / Approved (Behind Flag)"
        else:
            status = "Experimental / In Development (Behind Flag)"

        gated_status_matrix.append(
            {
                "subsystem": normalize_flag_name(flag_info.flag_name),
                "display_name": flag_info.display_name,
                "ctk_flag": flag_info.flag_name,
                "helm_flag": flag_info.flag_name,
                "is_active_ootb": is_active,
                "is_approved": is_approved,
                "status": status,
            }
        )

    # 6. Calculate Version Increment Recommendation
    version_rec = calculate_version_recommendation(base_ref, commits, transitions, categorized)

    result = {
        "metadata": {
            "base_ref": base_ref,
            "target_ref": target_ref,
            "commit_count": len(commits),
            "pr_count": len([c for c in commits if c.pr_number is not None]),
            "approved_flags": sorted(approved_flags),
        },
        "version_recommendation": asdict(version_rec),
        "flag_transitions": [asdict(t) for t in transitions],
        "gated_subsystems_status": gated_status_matrix,
        "categorized_commits": categorized,
        "raw_commits": [asdict(c) for c in commits],
    }

    if args.output:
        out_path = Path(args.output).resolve()
        out_path.parent.mkdir(parents=True, exist_ok=True)
        with open(out_path, "w", encoding="utf-8") as f:
            json.dump(result, f, indent=2)

    return result


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(
        description="Extracts structured release metadata, commit scopes, and feature flag classifications."
    )
    parser.add_argument(
        "--base-ref",
        "-b",
        help="Base git reference or tag (e.g., '0.5.0'). Defaults to tag preceding target-ref.",
    )
    parser.add_argument(
        "--target-ref",
        "-t",
        default="HEAD",
        help="Target git reference, branch, or tag (e.g., '0.6.0', 'HEAD'). Defaults to 'HEAD'.",
    )
    parser.add_argument(
        "--repo-dir",
        "-r",
        default=".",
        help="Path to XMC git repository root. Defaults to current directory.",
    )
    parser.add_argument(
        "--approved-flags",
        "-a",
        nargs="*",
        default=[],
        help="List of feature flags approved/production-ready for this release (e.g. 'rod_deployment_enabled').",
    )
    parser.add_argument(
        "--output",
        "-o",
        help="Optional path to write the output JSON file.",
    )
    run_tool(parser, extract_release_metadata)


if __name__ == "__main__":
    main()
