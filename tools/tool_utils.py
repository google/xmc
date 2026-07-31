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

"""Utility module for formatting tool execution output and exceptions."""

import argparse
import json
import logging
import os
import re
import subprocess
import sys
import traceback
from pathlib import Path
from typing import Any


def _should_bypass_venv() -> bool:
    """Checks if virtual environment activation should be skipped."""
    if "TEST_SRCDIR" in os.environ or "BAZEL_TEST" in os.environ:
        return True
    if sys.prefix != sys.base_prefix or os.environ.get("XMC_BYPASS_VENV") == "1":
        return True
    if "unittest" in sys.modules or "pytest" in sys.modules:
        return True
    file_path_str = str(Path(__file__).resolve())
    cwd_path_str = str(Path.cwd().resolve())
    return bool(
        any(
            token in file_path_str or token in cwd_path_str
            for token in ("execroot", "bazel-out", "sandbox")
        )
    )


def ensure_venv(venv_dir: Path | None = None) -> None:
    """Ensures the current process runs inside the tooling virtual environment."""
    if _should_bypass_venv():
        return

    resolved_venv = (
        venv_dir
        or Path(
            os.environ.get("TOOLS_VENV_DIR", Path(__file__).resolve().parent / "venv")
        ).resolve()
    )
    venv_python = resolved_venv / "bin" / "python3"
    if not venv_python.exists():
        venv_python = resolved_venv / "bin" / "python"

    if not venv_python.exists():
        if not os.access(resolved_venv.parent, os.W_OK):
            return
        try:
            _bootstrap_venv(resolved_venv)
        except Exception:
            logging.exception("Failed to bootstrap virtual environment")
            return
        venv_python = resolved_venv / "bin" / "python3"
        if not venv_python.exists():
            venv_python = resolved_venv / "bin" / "python"

    if venv_python.exists() and Path(sys.executable).resolve() != venv_python.resolve():
        os.execv(str(venv_python), [str(venv_python)] + sys.argv)


def _bootstrap_venv(venv_dir: Path) -> None:
    """Bootstraps the virtual environment if it does not yet exist."""
    os.environ.setdefault("PIP_INDEX_URL", "https://pypi.org/simple")
    setup_script = Path(__file__).resolve().parent / "setup_venv.py"
    if setup_script.is_file():
        subprocess.run(
            [sys.executable, str(setup_script), "--venv-dir", str(venv_dir)],
            check=True,
        )


class HandledError(Exception):
    """Exception raised for errors that are handled with instructions."""

    def __init__(self, message, instruction):
        super().__init__(message)
        self.message = message
        self.instruction = instruction


def run_tool(parser: argparse.ArgumentParser, main_fn):
    """Standard runner for tools.

    - Handles `--help` automatically via argparse.
    - Captures exceptions and formats output as JSON.
    - Exit codes:
      0: Success
      1: Handled error
      2: Unhandled error
    """
    ensure_venv()
    try:
        args = parser.parse_args()
        result = main_fn(args)

        output = {"status": "success", "result": result}
        print(json.dumps(output, indent=2))
        sys.exit(0)

    except HandledError as e:
        output = {
            "status": "error",
            "error_type": "handled",
            "message": e.message,
            "instruction": e.instruction,
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(1)

    except Exception as e:
        output = {
            "status": "error",
            "error_type": "unhandled",
            "message": str(e),
            "traceback": traceback.format_exc(),
        }
        print(json.dumps(output, indent=2), file=sys.stderr)
        sys.exit(2)


# Config validation schemas
CONFIG_VALIDATION_SCHEMAS = {
    "deployment_name": r"^[a-zA-Z0-9_-]+$",
    "project_id": r"^[a-z0-9:.-]+$",
    "environment": r"^[a-zA-Z0-9_-]+$",
    "region": r"^[a-z0-9-]+$",
    "domain_name": r"^[a-zA-Z0-9.-]+$",
    "image_registry": r"^[a-zA-Z0-9./:-]+$",
    "image_tag": r"^[a-zA-Z0-9_.-]+$",
}

# Environment variable overrides mapping
ENV_VAR_MAPPING = {
    "PROJECT_ID": "project_id",
    "XMC_DEPLOYMENT_NAME": "deployment_name",
    "GCP_REGION": "region",
    "XMANAGER_ENV": "environment",
    "K8S_NAMESPACE": "namespace",
    "XMC_ACR_TARGET": "image_registry",  # Alternative, processed first
    "IMAGE_REGISTRY": "image_registry",  # Primary, processed second (wins)
    "BUILD_TAG": "image_tag",
    "XMC_BASE_DOMAIN": "domain_name",
}


def _parse_yaml_vars_line_fallback(content: str) -> dict[str, Any]:
    """Parses key-value scalar entries under 'vars:' section in YAML content."""
    vars_dict: dict[str, Any] = {}
    in_vars_section = False
    for line in content.splitlines():
        trimmed = line.strip()
        if not trimmed or trimmed.startswith("#"):
            continue
        if line.startswith("vars:"):
            in_vars_section = True
            continue
        if in_vars_section:
            if line and not line.startswith(" ") and not line.startswith("\t"):
                break
            match = re.match(r"^\s*([a-zA-Z0-9_]+):\s*(.*)$", line)
            if match:
                key, val = match.group(1), match.group(2).strip()
                val = val.split(" #")[0].strip()
                if (val.startswith('"') and val.endswith('"')) or (
                    val.startswith("'") and val.endswith("'")
                ):
                    val = val[1:-1]
                vars_dict[key] = val
    return vars_dict


def parse_yaml_vars(config_path: Path) -> dict[str, Any]:
    """Reads YAML vars section using PyYAML or fallback line parsing."""
    if not config_path.is_file():
        raise FileNotFoundError(f"Configuration file '{config_path}' not found.")

    content = config_path.read_text(encoding="utf-8")
    try:
        import yaml  # pylint: disable=g-import-not-at-top

        data = yaml.safe_load(content)
        if isinstance(data, dict) and "vars" in data:
            vars_data = data.get("vars") or {}
            return {k: v for k, v in vars_data.items() if isinstance(v, (str, int, float, bool))}
    except Exception:  # pylint: disable=broad-except
        pass

    return _parse_yaml_vars_line_fallback(content)


def load_and_validate_config(
    config_file: str,
    required_keys: list[str] | None = None,
    defaults: dict[str, str] | None = None,
) -> dict[str, Any]:
    """Loads YAML config file, merges with env overrides, and validates.

    Args:
        config_file: Path to the YAML configuration file.
        required_keys: List of keys that MUST be present and valid. If None,
          defaults to all keys in CONFIG_VALIDATION_SCHEMAS.
        defaults: Default values for keys if they are missing.

    Returns:
        Dictionary of validated configuration.

    Raises:
        ValueError: If a required key is missing or invalid.
        FileNotFoundError: If the config file does not exist.
    """
    path = Path(config_file)
    yaml_vars = parse_yaml_vars(path)

    config: dict[str, Any] = {}
    if defaults:
        config.update(defaults)

    config.update(yaml_vars)

    for env_var, config_key in ENV_VAR_MAPPING.items():
        val = os.getenv(env_var)
        if val:
            config[config_key] = val

    keys_to_validate = (
        required_keys if required_keys is not None else list(CONFIG_VALIDATION_SCHEMAS.keys())
    )

    for key in keys_to_validate:
        val = config.get(key)
        if val is None:
            raise ValueError(f"Required configuration field '{key}' is missing.")

        pattern = CONFIG_VALIDATION_SCHEMAS.get(key)
        if pattern and not re.match(pattern, str(val)):
            raise ValueError(f"Field '{key}' value '{val}' is invalid. Must match regex: {pattern}")

    # Preserve other keys from YAML (like namespace, redis_region, etc.)
    for k, v in yaml_vars.items():
        if k not in config:
            config[k] = v

    # Also apply env overrides for keys that might not be in CONFIG_VALIDATION_SCHEMAS but mapped
    val_ns = os.getenv("K8S_NAMESPACE")
    if val_ns:
        config["namespace"] = val_ns

    return config
