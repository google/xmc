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
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass(kw_only=True)
class DeploymentConfig:
    """Base configuration dataclass shared across scripts."""

    project_id: str
    deployment_name: str
    region: str
    environment: str
    image_registry: str
    image_tag: str
    namespace: str = "xmc"
    domain_name: str | None = None
    enable_components: bool = False
    log_monitor_deployment_enabled: bool = False

    @property
    def gke_cluster(self) -> str:
        """Returns the GKE cluster name."""
        return f"xmc-{self.deployment_name}-{self.environment}"

    @property
    def registry(self) -> str:
        """Returns the Artifact Registry target repository."""
        return self.image_registry


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

    default_venv = Path(__file__).resolve().parent / "venv"
    if not default_venv.exists() and (Path(__file__).resolve().parent.parent / "venv").exists():
        default_venv = Path(__file__).resolve().parent.parent / "venv"

    resolved_venv = venv_dir or Path(os.environ.get("TOOLS_VENV_DIR", default_venv)).resolve()
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
        repo_root = Path(__file__).resolve().parent.parent.parent
        pythonpath = os.environ.get("PYTHONPATH", "")
        if str(repo_root) not in pythonpath.split(os.pathsep):
            os.environ["PYTHONPATH"] = (
                f"{repo_root}{os.pathsep}{pythonpath}" if pythonpath else str(repo_root)
            )
        os.execv(str(venv_python), [str(venv_python)] + sys.argv)


def _bootstrap_venv(venv_dir: Path) -> None:
    """Bootstraps the virtual environment if it does not yet exist."""
    os.environ.setdefault("PIP_INDEX_URL", "https://pypi.org/simple")
    setup_script = Path(__file__).resolve().parent / "setup_venv.py"
    if not setup_script.is_file():
        setup_script = Path(__file__).resolve().parent.parent / "setup_venv.py"
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
    "ENABLE_COMPONENTS": "enable_components",
    "LOG_MONITOR_DEPLOYMENT_ENABLED": "log_monitor_deployment_enabled",
}


def parse_bool_flag(val: Any) -> bool:
    """Parses a boolean configuration flag from a boolean or string."""
    return val.strip().lower() in ("true", "1", "yes") if isinstance(val, str) else bool(val)


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
                if (val.startswith('"') and val.count('"') >= 2) or (
                    val.startswith("'") and val.count("'") >= 2
                ):
                    quote_char = val[0]
                    end_quote_idx = val.find(quote_char, 1)
                    if end_quote_idx != -1:
                        val = val[1:end_quote_idx]
                else:
                    val = val.split(" #")[0].strip()
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
        if isinstance(data, dict):
            vars_data = data.get("vars") or {}
            return {k: v for k, v in vars_data.items() if isinstance(v, (str, int, float, bool))}
    except Exception:  # pylint: disable=broad-except
        pass

    return _parse_yaml_vars_line_fallback(content)


def load_and_validate_config(
    config_file: str,
    defaults: dict[str, str] | None = None,
) -> DeploymentConfig:
    """Loads YAML config file, merges with env overrides, and validates.

    Args:
        config_file: Path to the YAML configuration file.
        defaults: Default values for keys if they are missing.

    Returns:
        DeploymentConfig of validated configuration.

    Raises:
        ValueError: If a required key is missing or invalid.
        FileNotFoundError: If the config file does not exist.
    """
    path = Path(config_file)
    yaml_vars = parse_yaml_vars(path)

    config: dict[str, Any] = (defaults or {}) | yaml_vars

    for env_var, config_key in ENV_VAR_MAPPING.items():
        val = os.getenv(env_var)
        if val:
            config[config_key] = val

    for key in CONFIG_VALIDATION_SCHEMAS:
        val = config.get(key)
        if val is None:
            raise ValueError(f"Required configuration field '{key}' is missing.")

        pattern = CONFIG_VALIDATION_SCHEMAS.get(key)
        if pattern and not re.match(pattern, str(val)):
            raise ValueError(f"Field '{key}' value '{val}' is invalid. Must match regex: {pattern}")

    return DeploymentConfig(
        project_id=config["project_id"],
        deployment_name=config["deployment_name"],
        region=config["region"],
        environment=config["environment"],
        image_registry=config["image_registry"],
        image_tag=config["image_tag"],
        domain_name=config.get("domain_name"),
        namespace=config.get("namespace", "xmc"),
        enable_components=parse_bool_flag(config.get("enable_components", False)),
        log_monitor_deployment_enabled=parse_bool_flag(
            config.get("log_monitor_deployment_enabled", False)
        ),
    )


# Color constants for terminal logging
BLUE = "\033[0;34m"
GREEN = "\033[0;32m"
RED = "\033[0;31m"
NC = "\033[0m"  # No Color


def log_info(msg: str) -> None:
    """Logs informational messages in blue text."""
    print(f"\n{BLUE}[INFO] {msg}{NC}")


def log_success(msg: str) -> None:
    """Logs success messages in green text."""
    print(f"\n{GREEN}[SUCCESS] {msg}{NC}")


def log_error(msg: str) -> None:
    """Logs error messages in red text to stderr."""
    print(f"\n{RED}[ERROR] {msg}{NC}", file=sys.stderr)


def run_command(
    cmd: list[str],
    env: dict[str, str] | None = None,
    check: bool = True,
    capture_output: bool = False,
    log_on_error: bool = True,
    log_cmd: bool = True,
    cwd: Path | str | None = None,
) -> subprocess.CompletedProcess:
    """Helper to run shell commands with unified logging and error handling.

    Args:
        cmd: The command to run as a list of strings.
        env: Optional environment variables to merge with current env.
        check: Whether to raise CalledProcessError on non-zero exit code.
        capture_output: Whether to capture stdout/stderr.
        log_on_error: Whether to log errors on failure.
        log_cmd: Whether to log the command being executed.
        cwd: Optional working directory for subprocess execution.

    Returns:
        A subprocess.CompletedProcess object.

    Raises:
        subprocess.CalledProcessError: If the command fails and check=True.
    """
    if log_cmd:
        log_info(f"Running: {' '.join(cmd)}")

    proc_env = os.environ.copy() | {str(k): str(v) for k, v in (env or {}).items()}

    try:
        return subprocess.run(
            cmd,
            env=proc_env,
            check=check,
            text=True,
            capture_output=capture_output,
            cwd=str(cwd) if cwd else None,
        )
    except subprocess.CalledProcessError:
        if log_on_error:
            log_error(f"Command failed: {' '.join(cmd)}")
        raise


def configure_docker_auth(config: DeploymentConfig) -> None:
    """Configures Docker authentication for Google Artifact Registry if needed.

    Args:
        config: DeploymentConfig containing 'image_registry'.
    """
    image_registry = config.image_registry
    if not image_registry:
        raise ValueError("Required configuration field 'image_registry' is missing or empty.")

    log_info("Configuring Docker registry authorization...")
    registry_host = image_registry.split("/")[0]

    if registry_host.endswith(("gcr.io", ".pkg.dev")):
        run_command(["gcloud", "auth", "configure-docker", registry_host, "--quiet"])
    else:
        log_info(f"Non-GCP registry detected ({registry_host}). Skipping gcloud credential helper.")


def build_and_push_images(config: DeploymentConfig) -> None:
    """Rebuilds and pushes XMC microservice OCI images to Artifact Registry.

    Args:
        config: DeploymentConfig containing environment variables.
    """
    image_registry = config.image_registry
    environment = config.environment
    image_tag = config.image_tag

    if not image_registry:
        raise ValueError("Required configuration field 'image_registry' is missing or empty.")
    if not environment:
        raise ValueError("Required configuration field 'environment' is missing or empty.")
    if not image_tag:
        raise ValueError("Required configuration field 'image_tag' is missing or empty.")

    log_info(f"Rebuilding and pushing latest XMC microservice OCI images to '{image_registry}'...")
    env = {
        "XMC_ENV": environment,
        "XMC_ACR_TARGET": image_registry,
        "BUILD_TAG": image_tag,
    }

    targets = [
        ("actuator", "//cmd/actuator:actuator_image_push"),
        (
            "experiment_state_server",
            "//cmd/experiment_state_server:experiment_state_server_image_push",
        ),
        ("log_monitor", "//cmd/log_monitor:log_monitor_image_push"),
        ("reconciler", "//cmd/reconciler:reconciler_image_push"),
        ("ui", "//cmd/ui:ui_image_push"),
        ("xid_service", "//cmd/xid_service:xid_service_image_push"),
        ("health_service", "//cmd/health_service:health_service_image_push"),
        ("dashboard_service", "//cmd/dashboard_service:dashboard_service_image_push"),
        ("ingestion_gateway", "//cmd/ingestion_gateway:ingestion_gateway_image_push"),
        ("xmc_bridge", "//cmd/xmc_bridge:xmc_bridge_image_push"),
        ("rod_data_provider", "//cmd/rod_data_provider:rod_data_provider_image_push"),
    ]

    for name, target in targets:
        log_info(f"Pushing {name} image...")
        run_command(["bazel", "run", target], env=env)

    log_success("All XMC service container images compiled and pushed from current branch state.")


def deploy_component_profile(config: DeploymentConfig) -> None:
    """Deploys default XMC component profile (stable-2026a) when components are enabled."""
    if not config.enable_components:
        return
    ns = config.namespace or "xmc"
    chart = str(Path(__file__).resolve().parents[2] / "deploy" / "profiles" / "stable-2026a")
    log_info(f"Deploying default component profile (stable-2026a) into namespace '{ns}'...")
    run_command(["helm", "upgrade", "--install", "xmc-profile", chart, "-n", ns, "--wait"])
    log_success("Default component profile (stable-2026a) deployed.")
