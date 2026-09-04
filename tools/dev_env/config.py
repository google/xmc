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

"""Configuration loader and dataclass for XMC development environments."""

import argparse
import logging
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from tools.common.tool_utils import parse_yaml_vars

_LOGGER = logging.getLogger("xmc.dev_env.config")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

DEFAULT_CONFIG_LOCATIONS: list[Path] = [
    Path("deploy/ctk_v2/xmanager-cloud-deployment.yaml"),
    Path("deploy/local/xmanager-cloud-local.yaml"),
]

BASE_SERVICES: list[str] = [
    "experiment_state_server",
    "xid_service",
    "actuator",
    "reconciler",
    "ui",
]


@dataclass
class LocalDevConfig:
    """Configuration settings for local development environment."""

    project_id: str
    environment: str = "dev"
    spanner_instance: str = ""
    ess_database: str = ""
    xid_database: str = ""
    cluster_name: str = ""
    auto_migrate: bool = True
    enable_managed_mldiagnostics: bool = False
    managed_xprof_gcs_bucket: str = ""
    enabled_services: list[str] = field(default_factory=lambda: list(BASE_SERVICES))
    config_path: Path | None = None

    def __post_init__(self) -> None:
        """Applies dynamic defaults and validates required configuration fields."""
        if not self.project_id or not self.project_id.strip():
            raise ValueError(
                "Missing required configuration field: 'project_id'. "
                "Specify it via --project-id / -p, PROJECT_ID / GCP_PROJECT_ID "
                "environment variable, or in your configuration YAML file."
            )
        if self.managed_xprof_gcs_bucket:
            cleaned = self.managed_xprof_gcs_bucket.strip().rstrip("/")
            if not cleaned.startswith("gs://") or cleaned == "gs:":
                raise ValueError(
                    f"Invalid GCS bucket URI: '{self.managed_xprof_gcs_bucket}'. "
                    "The bucket URI must start with 'gs://' and specify a bucket name."
                )
            self.managed_xprof_gcs_bucket = cleaned
        if not self.spanner_instance:
            self.spanner_instance = f"xmc-{self.environment}"
        if not self.ess_database:
            self.ess_database = f"experiment-db-{self.environment}"
        if not self.xid_database:
            self.xid_database = f"xids-db-{self.environment}"
        if not self.cluster_name:
            self.cluster_name = f"xmc-{self.environment}"


def _parse_bool(val: Any) -> bool:
    """Parses a boolean value from bool, numeric, or string representations."""
    if isinstance(val, bool):
        return val
    if isinstance(val, (int, float)):
        return bool(val)
    if isinstance(val, str):
        return val.strip().lower() in ("true", "1", "yes", "y", "t", "on")
    return False


def _resolve_bool_flag(
    cli_val: Any,
    env_val: Any,
    yaml_val: Any,
    default: bool = False,
) -> bool:
    """Resolves a boolean flag applying precedence: CLI > Env > YAML > Default."""
    if cli_val is not None:
        return _parse_bool(cli_val)
    if env_val is not None:
        return _parse_bool(env_val)
    if yaml_val is not None:
        return _parse_bool(yaml_val)
    return default


def _clean_str(val: Any) -> str | None:
    """Returns stripped string if val is non-empty, else None."""
    if val is None:
        return None
    s = str(val).strip()
    return s if s else None


def _resolve_str_flag(
    cli_val: Any,
    env_val: Any,
    yaml_val: Any,
    default: str = "",
) -> str:
    """Resolves a string flag applying precedence: CLI > Env > YAML > Default."""
    for val in (cli_val, env_val, yaml_val):
        cleaned = _clean_str(val)
        if cleaned is not None:
            return cleaned
    return default


def resolve_config_path(
    explicit_path: str | Path | None = None,
    repo_root: Path | None = None,
) -> Path | None:
    """Resolves configuration file path via explicit input or fallback order.

    Resolution order:
    1. Explicit config path (must exist if specified).
    2. deploy/ctk_v2/xmanager-cloud-deployment.yaml (if exists).
    3. deploy/local/xmanager-cloud-local.yaml (if exists).
    4. None (pure fallback).

    Args:
        explicit_path: Explicit path to config file if provided.
        repo_root: Optional repository root for resolving relative paths.

    Returns:
        Resolved Path if found, or None if fallback order is exhausted.

    Raises:
        FileNotFoundError: If explicit_path is provided but does not exist.
    """
    root = repo_root or _REPO_ROOT
    if explicit_path is not None:
        clean_path = _clean_str(explicit_path)
        if clean_path:
            p = Path(clean_path)
            if not p.is_absolute() and (root / p).is_file():
                return (root / p).resolve()
            if p.is_file():
                return p.resolve()
            raise FileNotFoundError(f"Configuration file '{clean_path}' not found.")

    for candidate in DEFAULT_CONFIG_LOCATIONS:
        if (root / candidate).is_file():
            return (root / candidate).resolve()
    return None


def _resolve_enabled_services(
    args: argparse.Namespace | None,
    yaml_vars: dict[str, Any],
) -> list[str]:
    """Computes enabled services from base services and conditional feature flags."""
    services = list(BASE_SERVICES)

    enable_metrics = _resolve_bool_flag(
        getattr(args, "enable_metrics", None) if args else None,
        os.environ.get("ENABLE_METRICS"),
        yaml_vars.get("enable_metrics"),
        default=False,
    )
    if enable_metrics and "ingestion_gateway" not in services:
        services.append("ingestion_gateway")

    enable_components = _resolve_bool_flag(
        getattr(args, "enable_components", None) if args else None,
        os.environ.get("ENABLE_COMPONENTS"),
        yaml_vars.get("enable_components"),
        default=False,
    )
    if enable_components and "components" not in services:
        services.append("components")

    rod_enabled = _resolve_bool_flag(
        getattr(args, "rod_deployment_enabled", None) if args else None,
        os.environ.get("ROD_DEPLOYMENT_ENABLED"),
        yaml_vars.get("rod_deployment_enabled"),
        default=False,
    )
    if rod_enabled and "rod_data_provider" not in services:
        services.append("rod_data_provider")

    enable_health = _resolve_bool_flag(
        getattr(args, "enable_health_service", None) if args else None,
        os.environ.get("ENABLE_HEALTH_SERVICE"),
        yaml_vars.get(
            "enable_health_service",
            yaml_vars.get("health_service_deployment_enabled"),
        ),
        default=False,
    )
    if enable_health and "health_service" not in services:
        services.append("health_service")

    enable_dashboard = _resolve_bool_flag(
        getattr(args, "enable_dashboard_service", None) if args else None,
        os.environ.get("ENABLE_DASHBOARD_SERVICE"),
        yaml_vars.get(
            "enable_dashboard_service",
            yaml_vars.get("dashboard_service_deployment_enabled"),
        ),
        default=False,
    )
    if enable_dashboard and "dashboard_service" not in services:
        services.append("dashboard_service")

    return services


def load_config(
    args: argparse.Namespace | None = None,
    config_path: str | Path | None = None,
    repo_root: Path | None = None,
) -> LocalDevConfig:
    """Loads and resolves LocalDevConfig applying fallback precedence.

    Precedence order: CLI args > Environment variables > YAML file > Defaults.

    Args:
        args: Parsed command-line arguments.
        config_path: Optional explicit path to configuration YAML file.
        repo_root: Optional repository root for resolving relative config paths.

    Returns:
        Validated LocalDevConfig instance.

    Raises:
        FileNotFoundError: If explicitly specified configuration file does not exist.
        ValueError: If required configuration fields are missing or invalid.
    """
    explicit_config = config_path or (getattr(args, "config", None) if args else None)
    resolved_path = resolve_config_path(explicit_config, repo_root=repo_root)

    yaml_vars: dict[str, Any] = {}
    if resolved_path:
        yaml_vars = parse_yaml_vars(resolved_path)

    project_id = _resolve_str_flag(
        getattr(args, "project_id", None) if args else None,
        os.environ.get("PROJECT_ID") or os.environ.get("GCP_PROJECT_ID"),
        yaml_vars.get("project_id"),
        default="",
    )

    environment = _resolve_str_flag(
        getattr(args, "environment", None) if args else None,
        os.environ.get("XMANAGER_ENV") or os.environ.get("ENVIRONMENT"),
        yaml_vars.get("environment"),
        default="dev",
    )

    spanner_instance = _resolve_str_flag(
        getattr(args, "spanner_instance", None) if args else None,
        os.environ.get("SPANNER_INSTANCE"),
        yaml_vars.get("spanner_instance"),
        default="",
    )

    ess_database = _resolve_str_flag(
        getattr(args, "ess_database", None) if args else None,
        os.environ.get("ESS_DATABASE"),
        yaml_vars.get("ess_database"),
        default="",
    )

    xid_database = _resolve_str_flag(
        getattr(args, "xid_database", None) if args else None,
        os.environ.get("XID_DATABASE"),
        yaml_vars.get("xid_database"),
        default="",
    )

    cluster_name = _resolve_str_flag(
        getattr(args, "cluster_name", None) if args else None,
        os.environ.get("CLUSTER_NAME"),
        yaml_vars.get("cluster_name"),
        default="",
    )

    auto_migrate = _resolve_bool_flag(
        getattr(args, "auto_migrate", None) if args else None,
        os.environ.get("AUTO_MIGRATE"),
        yaml_vars.get("auto_migrate"),
        default=True,
    )

    enable_managed_mldiagnostics = _resolve_bool_flag(
        getattr(args, "enable_managed_mldiagnostics", None) if args else None,
        None,
        yaml_vars.get("enable_managed_mldiagnostics"),
        default=False,
    )

    managed_xprof_gcs_bucket = _resolve_str_flag(
        getattr(args, "managed_xprof_gcs_bucket", None) if args else None,
        None,
        yaml_vars.get("managed_xprof_gcs_bucket"),
        default="",
    )

    enabled_services = _resolve_enabled_services(args, yaml_vars)

    cfg = LocalDevConfig(
        project_id=project_id,
        environment=environment,
        spanner_instance=spanner_instance,
        ess_database=ess_database,
        xid_database=xid_database,
        cluster_name=cluster_name,
        auto_migrate=auto_migrate,
        enable_managed_mldiagnostics=enable_managed_mldiagnostics,
        managed_xprof_gcs_bucket=managed_xprof_gcs_bucket,
        enabled_services=enabled_services,
        config_path=resolved_path,
    )
    _LOGGER.debug(
        "Resolved LocalDevConfig: project_id=%s, environment=%s, spanner_instance=%s, cluster_name=%s, config_path=%s",
        cfg.project_id,
        cfg.environment,
        cfg.spanner_instance,
        cfg.cluster_name,
        cfg.config_path,
    )
    return cfg
