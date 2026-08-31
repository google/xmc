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

"""Smart change detection and service mapping for local KinD fast reload."""

import dataclasses
import logging
import subprocess
from pathlib import Path

from tools.dev_env.image_builder import discover_buildable_services

_LOGGER = logging.getLogger("xmc.dev_env.change_detector")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

# Canonical fallback microservices in XMC if repo cannot be scanned
ALL_CANONICAL_SERVICES: tuple[str, ...] = (
    "actuator",
    "components",
    "dashboard_service",
    "experiment_state_server",
    "health_service",
    "ingestion_gateway",
    "log_monitor",
    "reconciler",
    "rod_data_provider",
    "ui",
    "xid_service",
)

# Service alias resolution table
SERVICE_ALIASES: dict[str, str] = {
    "dashboard": "dashboard_service",
    "ess": "experiment_state_server",
    "gateway": "ingestion_gateway",
    "health": "health_service",
    "ingestion": "ingestion_gateway",
    "monitor": "log_monitor",
    "rod": "rod_data_provider",
    "xid": "xid_service",
}

# Explicit mappings for non-cmd source directories to canonical microservices
PACKAGE_PREFIX_MAP: dict[str, str] = {
    "internal/dashboard/": "dashboard_service",
    "api/protos/dashboard/": "dashboard_service",
    "internal/storage/spanner/": "experiment_state_server",
    "api/protos/experiment/": "experiment_state_server",
    "api/protos/health/": "health_service",
    "api/protos/metrics/": "ingestion_gateway",
    "api/protos/rod/": "rod_data_provider",
    "api/protos/xid/": "xid_service",
}

# Shared prefixes and root files that affect all microservices
SHARED_CODE_PREFIXES: tuple[str, ...] = (
    "internal/common/",
    "internal/auth/",
    "internal/configutil/",
    "internal/gcemetadata/",
    "internal/security/",
    "internal/logging/",
    "internal/kubernetes/",
    "internal/domain/",
    "internal/compat/",
    "internal/validator/",
    "internal/task_queue/",
    "internal/task_queue_redis/",
    "internal/toollibs/",
    "api/protos/components/",
    "api/protos/task_queue/",
    "pkg/",
)
SHARED_ROOT_FILES: set[str] = {
    "go.mod",
    "go.sum",
    "WORKSPACE",
    "MODULE.bazel",
    "BUILD.bazel",
    "BUILD",
    ".bazelrc",
}
HELM_MANIFEST_PREFIXES: tuple[str, ...] = ("deploy/helm/", "deploy/local/", "test/e2e/manifests/")


@dataclasses.dataclass(kw_only=True)
class ChangeDetectionResult:
    """Encapsulates change detection analysis results."""

    services: list[str] = dataclasses.field(default_factory=list)
    helm_upgrade_needed: bool = False
    modified_files: list[str] = dataclasses.field(default_factory=list)
    reasons: dict[str, list[str]] = dataclasses.field(default_factory=dict)


def resolve_service_name(name: str) -> str:
    """Resolves a service alias or short name into its canonical identifier.

    Handles common developer aliases (e.g. 'ess' -> 'experiment_state_server',
    'xid' -> 'xid_service', 'dashboard' -> 'dashboard_service').

    Args:
        name: Raw service name or alias string.

    Returns:
        Canonical microservice name.
    """
    clean = name.strip().lower()
    return SERVICE_ALIASES.get(clean, clean)


def resolve_service_names(names: list[str] | None = None) -> list[str]:
    """Resolves a list of service names or aliases into unique canonical names.

    Filters out reserved keywords ('auto', 'all'), resolves aliases, deduplicates
    entries, and returns a sorted list of canonical service names.

    Args:
        names: List of service name or alias strings.

    Returns:
        Sorted list of unique canonical service names.
    """
    if not names:
        return []
    reserved = {"auto", "all"}
    return sorted(
        {
            resolve_service_name(n)
            for n in names
            if n and n.strip() and n.strip().lower() not in reserved
        }
    )


class ChangeDetector:
    """Detects modified files and maps them to affected XMC microservices and deployment needs."""

    def __init__(
        self,
        repo_root: Path | None = None,
        enabled_services: list[str] | None = None,
    ) -> None:
        """Initializes ChangeDetector with repository root and active services.

        Args:
            repo_root: Optional repository root Path; defaults to parent repository root.
            enabled_services: Optional list of active microservice names to scope detection to.
        """
        self.repo_root = repo_root or _REPO_ROOT
        if enabled_services is not None:
            self.enabled_services = resolve_service_names(enabled_services)
        else:
            discovered = discover_buildable_services(self.repo_root)
            self.enabled_services = discovered or list(ALL_CANONICAL_SERVICES)

    def _run_git(self, cmd: list[str]) -> list[str]:
        """Executes a git command and returns stdout lines stripped of trailing whitespace.

        Args:
            cmd: Command line argument list starting with 'git'.

        Returns:
            List of non-empty stdout lines, or empty list on command failure.
        """
        try:
            p = subprocess.run(
                cmd,
                cwd=str(self.repo_root),
                capture_output=True,
                text=True,
                check=False,
                timeout=15,
            )
            if p.returncode != 0:
                _LOGGER.warning(
                    "Git command %s failed with exit code %d: %s",
                    cmd,
                    p.returncode,
                    p.stderr,
                )
                return []
            return [line.rstrip() for line in p.stdout.splitlines() if line.strip()]
        except (subprocess.SubprocessError, OSError) as e:
            _LOGGER.warning("Failed to run git command %s: %s", cmd, e)
            return []

    def get_modified_files(self, base_ref: str | None = None) -> list[str]:
        """Inspects git status and diffs to discover modified and untracked files.

        Combines:
        1. Unstaged and staged working tree modifications (`git status --porcelain`).
        2. Renamed files (parsing `old -> new` porcelain notation).
        3. Committed changes between base_ref and HEAD (if base_ref provided).

        Args:
            base_ref: Optional git commit/branch ref to compare HEAD against.

        Returns:
            Sorted list of relative file paths modified in the repository.
        """
        files: set[str] = set()
        for line in self._run_git(["git", "-c", "core.quotepath=false", "status", "--porcelain"]):
            path_str = line[3:].strip().strip('"') if len(line) > 3 else ""
            if " -> " in path_str:
                path_str = path_str.split(" -> ", 1)[1].strip().strip('"')
            if path_str:
                files.add(path_str)
        diff_cmd = [
            "git",
            "-c",
            "core.quotepath=false",
            "diff",
            "--name-only",
            f"{base_ref}...HEAD" if base_ref else "HEAD",
        ]
        for line in self._run_git(diff_cmd):
            path_str = line.strip().strip('"')
            if path_str:
                files.add(path_str)
        res = sorted(files)
        _LOGGER.debug("Discovered modified files from git: %s", res)
        return res

    def _match_service_for_file(self, clean_f: str) -> str | None:
        """Matches a file path to its owning microservice if applicable."""
        if clean_f.startswith("cmd/"):
            parts = clean_f.split("/")
            if len(parts) > 1 and parts[1] in self.enabled_services:
                return parts[1]

        for prefix, svc in PACKAGE_PREFIX_MAP.items():
            if clean_f.startswith(prefix) and svc in self.enabled_services:
                return svc
        return None

    def _is_shared_code(self, clean_f: str) -> bool:
        """Determines if a file is a shared build file or common library."""
        return (
            clean_f in SHARED_ROOT_FILES
            or clean_f.endswith(("/BUILD.bazel", "/BUILD", ".bzl"))
            or any(clean_f.startswith(p) for p in SHARED_CODE_PREFIXES)
        )

    def detect_changes(
        self,
        files: list[str] | None = None,
        base_ref: str | None = None,
    ) -> ChangeDetectionResult:
        """Analyzes modified files and determines affected services and Helm requirements.

        Categorizes each changed file:
        - Helm / local manifests (deploy/helm/, test/e2e/manifests/): flags `helm_upgrade_needed`.
        - Service-specific directory prefixes (cmd/ui/, cmd/ess/, api/protos/...): maps to specific service.
        - Shared code / core build files (WORKSPACE, go.mod, internal/common/, BUILD.bazel):
          maps changes to all enabled microservices.

        Args:
            files: Optional explicit list of modified file paths. If None, queries git.
            base_ref: Optional base git reference to evaluate diffs against.

        Returns:
            ChangeDetectionResult containing affected services, Helm upgrade flags,
            and triggering file reasons per service.
        """
        modified = files if files is not None else self.get_modified_files(base_ref=base_ref)
        affected_services: set[str] = set()
        reasons: dict[str, list[str]] = {}
        helm_upgrade_needed = False

        for f in modified:
            clean_f = f.strip().replace("\\", "/")
            if not clean_f:
                continue

            if any(clean_f.startswith(p) for p in HELM_MANIFEST_PREFIXES):
                if clean_f.endswith((".yaml", ".yml", ".tpl", ".json")):
                    helm_upgrade_needed = True

            matched_service = self._match_service_for_file(clean_f)
            if matched_service:
                affected_services.add(matched_service)
                reasons.setdefault(matched_service, []).append(clean_f)
                continue

            if self._is_shared_code(clean_f):
                for svc in self.enabled_services:
                    affected_services.add(svc)
                    reasons.setdefault(svc, []).append(clean_f)

        result = ChangeDetectionResult(
            services=sorted(affected_services),
            helm_upgrade_needed=helm_upgrade_needed,
            modified_files=modified,
            reasons=reasons,
        )
        _LOGGER.debug(
            "Change detection result: services=%s, helm_needed=%s, reasons=%s",
            result.services,
            result.helm_upgrade_needed,
            result.reasons,
        )
        return result
