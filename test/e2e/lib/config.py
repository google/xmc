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

"""Configuration data models and CLI parsing for E2E orchestration."""

from __future__ import annotations

import argparse
import dataclasses
import enum
import logging
import os
import shutil
import tempfile
import time
import uuid

logger = logging.getLogger("xmc_e2e_runner")

# Centralized Default Test Credentials and User Personas
DEFAULT_ADMIN_USER = "admin@xmc.local"
DEFAULT_ADMIN_PASSWORD = "password123"
DEFAULT_VIEWER_USER = "viewer@xmc.local"
DEFAULT_VIEWER_PASSWORD = "password123"
DEFAULT_UNAUTHORIZED_USER = "unauthorized@xmc.local"
DEFAULT_UNAUTHORIZED_PASSWORD = "password123"

# Developer / Admin alias
DEFAULT_DEV_EMAIL = DEFAULT_ADMIN_USER
DEFAULT_DEV_PASSWORD = DEFAULT_ADMIN_PASSWORD

# Default Endpoints and OIDC Configuration
DEFAULT_BASE_URL = os.environ.get("XMC_BASE_URL", "http://localhost:8080")
DEFAULT_DEX_URL = os.environ.get("XMC_DEX_URL", "http://localhost:5556")
DEFAULT_DEX_CLIENT_ID = "xmc-client"
DEFAULT_DEX_CLIENT_SECRET = "xmc-client-secret"


class E2ETier(str, enum.Enum):
    """Execution tier determining virtualenv dependency set."""

    UI = "ui"
    WORKLOAD = "workload"


E2E_UI_VENV_DEPENDENCIES: list[str] = [
    "playwright",
    "requests",
]

E2E_WORKLOAD_VENV_DEPENDENCIES: list[str] = [
    "playwright",
    "requests",
    "grpcio",
    "protobuf",
    "absl-py",
    "python-dotenv",
    "google-auth",
    "xmanager",
]

# Default Tier-1 UI dependency set
E2E_VENV_DEPENDENCIES: list[str] = E2E_UI_VENV_DEPENDENCIES


def resolve_kind_binary(explicit_bin: str | None = None) -> str:
    """Resolves kind binary from explicit arg, KIND_BIN env, or PATH."""
    if explicit_bin:
        return os.path.expanduser(explicit_bin)

    env_bin = os.environ.get("KIND_BIN")
    if env_bin and os.path.isfile(os.path.expanduser(env_bin)):
        return os.path.expanduser(env_bin)

    which_bin = shutil.which("kind")
    if which_bin and os.path.isfile(which_bin):
        return which_bin

    raise FileNotFoundError(
        "Failed to locate 'kind' executable. Please ensure kind is installed in $PATH, "
        "set $KIND_BIN, or pass --kind-bin."
    )


@dataclasses.dataclass
class E2EConfig:
    """Configuration options for the E2E test lifecycle."""

    project_id: str
    spanner_instance: str
    cluster_name: str
    kind_bin: str
    workspace_root: str
    screenshots_dir: str
    kind_node_image: str = ""
    keep_cluster: bool = False
    skip_build: bool = False
    test_script: str | None = None
    tier: E2ETier = E2ETier.UI
    ess_db: str = ""
    xid_db: str = ""
    temp_dir: str = ""

    def __post_init__(self) -> None:
        if isinstance(self.tier, str):
            self.tier = E2ETier(self.tier)
        ts = int(time.time())
        suffix = uuid.uuid4().hex[:6]
        if not self.ess_db:
            self.ess_db = f"e2e-ess-{ts}-{suffix}"
        if not self.xid_db:
            self.xid_db = f"e2e-xid-{ts}-{suffix}"
        if not self.temp_dir:
            self.temp_dir = os.path.join(tempfile.gettempdir(), f"xmc_e2e_{suffix}")

    @property
    def kube_context(self) -> str:
        return f"kind-{self.cluster_name}"

    @property
    def venv_dependencies(self) -> list[str]:
        if self.tier == E2ETier.WORKLOAD:
            return list(E2E_WORKLOAD_VENV_DEPENDENCIES)
        return list(E2E_UI_VENV_DEPENDENCIES)


def parse_args(args: list[str] | None = None) -> E2EConfig:
    """Parses command-line arguments and environment variables into E2EConfig."""
    parser = argparse.ArgumentParser(
        description="Non-Agentic Headless KinD E2E Runner for XMC",
    )
    parser.add_argument(
        "--project-id",
        default=os.environ.get(
            "GCP_E2E_PROJECT_ID",
            os.environ.get("GCP_PROJECT_ID", "xmc-test-project-3"),
        ),
        help="GCP Project ID hosting Spanner instance (default: $GCP_E2E_PROJECT_ID or xmc-test-project-3)",
    )
    parser.add_argument(
        "--spanner-instance",
        default=os.environ.get(
            "GCP_E2E_SPANNER_INSTANCE",
            os.environ.get("SPANNER_INSTANCE", "xmc-e2e"),
        ),
        help="Spanner instance name (default: $GCP_E2E_SPANNER_INSTANCE or xmc-e2e)",
    )
    parser.add_argument(
        "--cluster-name",
        default=os.environ.get("KIND_CLUSTER_NAME", "xmc-e2e"),
        help="KinD cluster name (default: $KIND_CLUSTER_NAME or xmc-e2e)",
    )
    parser.add_argument(
        "--kind-bin",
        default=None,
        help="Path to kind executable (default: resolved from KIND_BIN, PATH, or candidate locations)",
    )
    parser.add_argument(
        "--workspace-root",
        default=os.environ.get(
            "WORKSPACE_ROOT",
            os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "..")),
        ),
        help="Workspace root directory path",
    )
    parser.add_argument(
        "--screenshots-dir",
        default=os.environ.get(
            "XMC_SCREENSHOTS_DIR",
            os.path.abspath("e2e-screenshots"),
        ),
        help="Directory to save Playwright test screenshot artifacts",
    )
    parser.add_argument(
        "--kind-node-image",
        default=os.environ.get("XMC_KIND_NODE_IMAGE", ""),
        help="Custom KinD node container image (default: $XMC_KIND_NODE_IMAGE or upstream default)",
    )
    parser.add_argument(
        "--keep-cluster",
        action="store_true",
        help="Keep KinD cluster and Spanner DB intact on exit for debugging",
    )
    parser.add_argument(
        "--skip-build",
        action="store_true",
        help="Skip container image building (assumes images loaded or available)",
    )
    parser.add_argument(
        "--test-script",
        default=os.environ.get("XMC_E2E_TEST_SCRIPT"),
        help="Path to custom test script to execute inside the virtualenv (default: test/e2e/test_e2e_flow.py)",
    )
    parser.add_argument(
        "--tier",
        choices=[t.value for t in E2ETier],
        default=os.environ.get("XMC_E2E_TIER", E2ETier.UI.value),
        help="E2E test tier determining dependency set ('ui' or 'workload', default: ui)",
    )

    parsed = parser.parse_args(args)
    return E2EConfig(
        project_id=parsed.project_id,
        spanner_instance=parsed.spanner_instance,
        cluster_name=parsed.cluster_name,
        kind_bin=resolve_kind_binary(parsed.kind_bin),
        workspace_root=parsed.workspace_root,
        screenshots_dir=parsed.screenshots_dir,
        kind_node_image=parsed.kind_node_image,
        keep_cluster=parsed.keep_cluster,
        skip_build=parsed.skip_build,
        test_script=parsed.test_script,
        tier=E2ETier(parsed.tier),
    )
