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
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from tools.common.tool_utils import parse_yaml_vars

_LOGGER = logging.getLogger("xmc.dev_env.config")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

DEFAULT_CONFIG_LOCATIONS: list[Path] = [
    Path("deploy/ctk_v2/xmanager-cloud-deployment.yaml"),
    Path("deploy/local/xmanager-cloud-local.yaml"),
]


@dataclass
class LocalDevConfig:
    """Configuration settings for local development environment."""

    project_id: str
    environment: str = "dev"
    config_path: Path | None = None

    def __post_init__(self) -> None:
        """Validates required configuration fields."""
        if not self.project_id or not self.project_id.strip():
            raise ValueError(
                "Missing required configuration field: 'project_id'. "
                "Specify it via --project-id / -p, PROJECT_ID / GCP_PROJECT_ID "
                "environment variable, or in your configuration YAML file."
            )


def _clean_str(val: Any) -> str | None:
    """Returns stripped string if val is non-empty, else None."""
    if val is None:
        return None
    s = str(val).strip()
    return s if s else None


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

    project_id = (
        _clean_str(getattr(args, "project_id", None) if args else None)
        or _clean_str(os.environ.get("PROJECT_ID"))
        or _clean_str(os.environ.get("GCP_PROJECT_ID"))
        or _clean_str(yaml_vars.get("project_id"))
        or ""
    )

    environment = (
        _clean_str(getattr(args, "environment", None) if args else None)
        or _clean_str(os.environ.get("XMANAGER_ENV"))
        or _clean_str(os.environ.get("ENVIRONMENT"))
        or _clean_str(yaml_vars.get("environment"))
        or "dev"
    )

    cfg = LocalDevConfig(
        project_id=project_id,
        environment=environment,
        config_path=resolved_path,
    )
    _LOGGER.debug(
        "Resolved LocalDevConfig: project_id=%s, environment=%s, config_path=%s",
        cfg.project_id,
        cfg.environment,
        cfg.config_path,
    )
    return cfg
