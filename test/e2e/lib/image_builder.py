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

"""Parallel container image building via Bazel and Docker loading."""

from __future__ import annotations

import logging
import os

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


class ImageBuilder:
    """Builds and tags OCI container images via Bazel and Docker."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def build_all(self) -> list[str]:
        """Builds all backend and frontend images."""
        if self.cfg.skip_build:
            logger.info("=== [Bazel] Skipping image builds (--skip-build set) ===")
            return [
                "experiment_state_server_image:latest",
                "xid_service_image:latest",
                "ui-image:latest",
                "actuator_image:latest",
                "reconciler_image:latest",
            ]

        logger.info("=== [Bazel] Building OCI images ===")
        services = [
            (
                "cmd/experiment_state_server",
                "experiment_state_server_image_tarball",
                "experiment_state_server_image:latest",
            ),
            (
                "cmd/xid_service",
                "xid_service_image_tarball",
                "xid_service_image:latest",
            ),
            ("cmd/ui", "ui_image_tarball", "ui-image:latest"),
            ("cmd/actuator", "actuator_image_tarball", "actuator_image:latest"),
            (
                "cmd/reconciler",
                "reconciler_image_tarball",
                "reconciler_image:latest",
            ),
        ]

        logger.info("Building all service images in parallel with Bazel...")
        bazel_targets = [f"//{pkg}:{target}" for pkg, target, _ in services]
        run_cmd(["bazel", "build", *bazel_targets], cwd=self.cfg.workspace_root)

        images: list[str] = []
        for pkg, target, tag in services:
            tar_path = os.path.join(self.cfg.workspace_root, "bazel-bin", pkg, f"{target}.tar")
            run_cmd(["docker", "load", "-i", tar_path])
            images.append(tag)

        return images
