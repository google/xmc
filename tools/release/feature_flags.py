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

"""Feature flag discovery, token normalization, and transition extraction."""

import re
import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.git_ops import get_git_file_content
    from tools.release.models import FeatureFlagInfo, FlagTransition
    from tools.release.yaml_parser import parse_yaml_booleans
except ImportError:
    from git_ops import get_git_file_content  # type: ignore[no-redef]
    from models import FeatureFlagInfo, FlagTransition  # type: ignore[no-redef]
    from yaml_parser import parse_yaml_booleans  # type: ignore[no-redef]

_CTK_CONFIG_FILES = [
    "deploy/ctk_v2/xmanager-cloud-deployment.yaml",
    "deploy/ctk_v2/xmanager-cloud.yaml",
    "deploy/ctk_v2/xmc-deployment-template.yaml",
]
_HELM_CONFIG_FILE = "deploy/helm/values.yaml"


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

    if "rod" in core:
        tokens.update(
            [
                "rod",
                "rod_data_provider",
                "rod-data-provider",
                "rod-ui",
                "rod-backend",
            ]
        )
    if "health" in core:
        tokens.update(
            [
                "health",
                "health_service",
                "health-service",
                "admin_panel",
                "admin-panel",
            ]
        )
    if "dashboard" in core or "dash" in core:
        tokens.update(
            [
                "dashboard",
                "dashboards",
                "xdash",
                "dashboard_service",
                "dashboard-service",
            ]
        )
    if "metric" in core or "ingestion" in core:
        tokens.update(
            [
                "metrics",
                "metric",
                "ingestion",
                "ingestion_gateway",
                "metrics_ingestion",
            ]
        )
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


def _load_flags_from_ref(ref: str, repo_dir: Path) -> tuple[dict[str, bool], dict[str, bool]]:
    """Loads CTK and Helm boolean flags for a specific git reference."""
    ctk_flags: dict[str, bool] = {}
    for rel_path in _CTK_CONFIG_FILES:
        content = get_git_file_content(ref, rel_path, repo_dir)
        if content:
            ctk_flags.update(parse_yaml_booleans(content))

    helm_content = get_git_file_content(ref, _HELM_CONFIG_FILE, repo_dir)
    helm_flags = parse_yaml_booleans(helm_content) if helm_content else {}
    return ctk_flags, helm_flags


def discover_feature_flags(target_ref: str, repo_dir: Path) -> dict[str, FeatureFlagInfo]:
    """Dynamically extracts all feature flags from CTK and Helm configs at target ref."""
    ctk_flags, helm_flags = _load_flags_from_ref(target_ref, repo_dir)
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

        info = FeatureFlagInfo(
            flag_name=flag,
            display_name=derive_display_name(flag),
            tokens=derive_flag_tokens(flag),
            ctk_value=ctk_val,
            helm_value=helm_val,
            is_active=bool(ctk_val) or bool(helm_val),
        )
        discovered[core] = info
        discovered[flag] = info

    return discovered


def get_current_feature_states(target_ref: str, repo_dir: Path) -> dict[str, bool]:
    """Extracts the active boolean status of dynamically discovered feature flags."""
    flags = discover_feature_flags(target_ref, repo_dir)
    return {k: info.is_active for k, info in flags.items()}


def extract_flag_transitions(
    base_ref: str, target_ref: str, repo_dir: Path
) -> list[FlagTransition]:
    """Compares CTK and Helm configuration files between base and target refs."""
    config_files = _CTK_CONFIG_FILES + [_HELM_CONFIG_FILE]
    transitions: list[FlagTransition] = []
    seen_flags: set[str] = set()

    for rel_path in config_files:
        base_content = get_git_file_content(base_ref, rel_path, repo_dir)
        target_content = get_git_file_content(target_ref, rel_path, repo_dir)
        if not base_content and not target_content:
            continue

        base_flags = parse_yaml_booleans(base_content) if base_content else {}
        target_flags = parse_yaml_booleans(target_content) if target_content else {}
        all_keys = sorted(set(base_flags.keys()) | set(target_flags.keys()))

        for k in all_keys:
            if k in seen_flags:
                continue
            base_val = base_flags.get(k)
            target_val = target_flags.get(k)
            if base_val != target_val:
                seen_flags.add(k)
                is_flip = base_val is not None and target_val is not None and base_val != target_val
                is_promotion = base_val is False and target_val is True
                if is_promotion:
                    desc = f"Promoted {k} to default true (enabled out-of-the-box)"
                elif is_flip:
                    desc = f"Flipped {k} from {base_val} to {target_val}"
                elif base_val is None:
                    desc = f"Introduced new configuration toggle {k} (default: {target_val})"
                else:
                    desc = f"Removed configuration toggle {k}"

                transitions.append(
                    FlagTransition(
                        flag_name=k,
                        source_file=rel_path,
                        base_value=base_val,
                        target_value=target_val,
                        is_flip=is_flip,
                        is_promotion_to_default=is_promotion,
                        description=desc,
                    )
                )

    return transitions
