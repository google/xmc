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
from pathlib import Path

from tools.common.changed_files import ChangedFilesError, list_changed_files
from tools.dev_env.image_builder import discover_buildable_services
from tools.dev_env.service_resolver import (
    SERVICE_ALIASES,
    resolve_service_name,
    resolve_service_names,
)

__all__ = [
    "ChangeDetectionResult",
    "ChangeDetector",
    "HELM_MANIFEST_PREFIXES",
    "SERVICE_ALIASES",
    "SHARED_CODE_PREFIXES",
    "SHARED_ROOT_FILES",
    "resolve_service_name",
    "resolve_service_names",
]

_LOGGER = logging.getLogger("xmc.dev_env.change_detector")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

# Shared prefixes and root build configuration files that affect all microservices
SHARED_CODE_PREFIXES: tuple[str, ...] = ("internal/", "api/", "pkg/")
SHARED_ROOT_FILES: frozenset[str] = frozenset(
    {
        "go.mod",
        "go.sum",
        "WORKSPACE",
        "MODULE.bazel",
        "BUILD.bazel",
        "BUILD",
        ".bazelrc",
    }
)
HELM_MANIFEST_PREFIXES: tuple[str, ...] = ("deploy/helm/", "deploy/local/", "test/e2e/manifests/")


@dataclasses.dataclass(kw_only=True)
class ChangeDetectionResult:
    """Encapsulates change detection analysis results."""

    services: list[str] = dataclasses.field(default_factory=list)
    helm_upgrade_needed: bool = False
    modified_files: list[str] = dataclasses.field(default_factory=list)
    reasons: dict[str, list[str]] = dataclasses.field(default_factory=dict)


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
            self.enabled_services = discover_buildable_services(self.repo_root)

    def get_modified_files(self, base_ref: str | None = None) -> list[str]:
        """Inspects git to discover modified and untracked files.

        Delegates to `tools.common.changed_files.list_changed_files`, which combines:
        1. Committed, staged, and unstaged changes to tracked files relative to the merge base
           of base_ref and HEAD (relative to HEAD if base_ref is not provided).
        2. Untracked files that git does not ignore.
        Deleted files are included, and a rename lists both its old and new path.

        Args:
            base_ref: Optional git commit/branch ref to compare HEAD against.

        Returns:
            Sorted list of relative file paths modified in the repository, or an empty list
            if git fails.
        """
        try:
            res = list_changed_files(base_ref, self.repo_root)
        except ChangedFilesError as e:
            _LOGGER.warning("Failed to discover modified files from git: %s", e.message)
            return []
        _LOGGER.debug("Discovered modified files from git: %s", res)
        return res

    def _match_service_for_file(self, clean_f: str) -> str | None:
        """Matches a file path to its owning microservice under cmd/<service>."""
        if clean_f.startswith("cmd/"):
            parts = clean_f.split("/")
            if len(parts) > 1:
                for svc in (parts[1], resolve_service_name(parts[1])):
                    if svc in self.enabled_services:
                        return svc
        return None

    def _is_shared_code(self, clean_f: str) -> bool:
        """Determines if a file is a root build configuration or shared Go library."""
        return clean_f in SHARED_ROOT_FILES or any(
            clean_f.startswith(p) for p in SHARED_CODE_PREFIXES
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
