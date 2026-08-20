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


def _get_service_specs() -> list[tuple[str, str, str]]:
    """Generates Bazel package, target, and image tag specifications."""
    standard_services = [
        "experiment_state_server",
        "xid_service",
        "actuator",
        "reconciler",
    ]
    specs = [
        (f"cmd/{name}", f"{name}_image_tarball", f"{name}_image:latest")
        for name in standard_services
    ]
    # UI frontend builder produces ui-image:latest
    specs.append(("cmd/ui", "ui_image_tarball", "ui-image:latest"))
    return specs


class ImageBuilder:
    """Builds and tags OCI container images via Bazel and Docker."""

    _SERVICES = _get_service_specs()

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def build_all(self) -> list[str]:
        """Builds all backend and frontend images."""
        if self.cfg.skip_build:
            logger.info("=== [Bazel] Skipping image builds (--skip-build set) ===")
            return [tag for _, _, tag in self._SERVICES]

        logger.info("=== [Bazel] Building OCI images ===")
        logger.info("Building all service images in parallel with Bazel...")
        bazel_targets = [f"//{pkg}:{target}" for pkg, target, _ in self._SERVICES]
        run_cmd(["bazel", "build", *bazel_targets], cwd=self.cfg.workspace_root)

        bazel_bin_res = run_cmd(
            ["bazel", "info", "bazel-bin"], cwd=self.cfg.workspace_root, capture_output=True
        )
        bazel_bin = bazel_bin_res.stdout.strip()

        images: list[str] = []
        for pkg, target, tag in self._SERVICES:
            tar_path = os.path.join(bazel_bin, pkg, f"{target}.tar")
            run_cmd(["docker", "load", "-i", tar_path])
            images.append(tag)

        return images
