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
import os
import shutil
import tempfile
import time
import uuid


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
    image_load_workers: int = 2
    keep_cluster: bool = False
    skip_build: bool = False
    ess_db: str = ""
    xid_db: str = ""
    temp_dir: str = ""

    def __post_init__(self) -> None:
        ts = int(time.time())
        suffix = uuid.uuid4().hex[:6]
        if not self.ess_db:
            self.ess_db = f"e2e-ess-{ts}-{suffix}"
        if not self.xid_db:
            self.xid_db = f"e2e-xid-{ts}-{suffix}"
        if not self.temp_dir:
            self.temp_dir = os.path.join(tempfile.gettempdir(), f"xmc_e2e_{suffix}")


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
        default=os.environ.get("KIND_BIN", shutil.which("kind") or "kind"),
        help="Path to kind executable (default: $KIND_BIN or which kind)",
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
        "--image-load-workers",
        type=int,
        default=int(os.environ.get("XMC_IMAGE_LOAD_WORKERS", "2")),
        help="Max workers for concurrent KinD image loading (default: $XMC_IMAGE_LOAD_WORKERS or 2)",
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

    parsed = parser.parse_args(args)
    return E2EConfig(
        project_id=parsed.project_id,
        spanner_instance=parsed.spanner_instance,
        cluster_name=parsed.cluster_name,
        kind_bin=parsed.kind_bin,
        workspace_root=parsed.workspace_root,
        screenshots_dir=parsed.screenshots_dir,
        kind_node_image=parsed.kind_node_image,
        image_load_workers=parsed.image_load_workers,
        keep_cluster=parsed.keep_cluster,
        skip_build=parsed.skip_build,
    )
