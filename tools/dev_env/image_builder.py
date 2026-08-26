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

"""Parallel container image building via Bazel and Docker/KinD loading."""

from __future__ import annotations

from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

_SERVICE_TARGET_MAP: dict[str, tuple[str, str, str]] = {
    "actuator": ("cmd/actuator", "actuator_image_tarball", "actuator_image:latest"),
    "experiment_state_server": (
        "cmd/experiment_state_server",
        "experiment_state_server_image_tarball",
        "experiment_state_server_image:latest",
    ),
    "log_monitor": (
        "cmd/log_monitor",
        "log_monitor_image_tarball",
        "log_monitor_image:latest",
    ),
    "reconciler": (
        "cmd/reconciler",
        "reconciler_image_tarball",
        "reconciler_image:latest",
    ),
    "ui": ("cmd/ui", "ui_image_tarball", "ui-image:latest"),
    "xid_service": (
        "cmd/xid_service",
        "xid_service_image_tarball",
        "xid_service_image:latest",
    ),
    "health_service": (
        "cmd/health_service",
        "health_service_image_tarball",
        "health_service_image:latest",
    ),
    "dashboard_service": (
        "cmd/dashboard_service",
        "dashboard_service_image_tarball",
        "dashboard_service_image:latest",
    ),
    "ingestion_gateway": (
        "cmd/ingestion_gateway",
        "ingestion_gateway_image_tarball",
        "ingestion_gateway_image:latest",
    ),
    "rod_data_provider": (
        "cmd/rod_data_provider",
        "rod_data_provider_image_tarball",
        "rod_data_provider_image:latest",
    ),
}


class ImageBuilder:
    """Builds OCI container images with Bazel and loads them into Docker and KinD."""

    def __init__(self, config: LocalDevConfig, repo_root: Path | None = None) -> None:
        """Initializes ImageBuilder with local dev environment configuration.

        Args:
            config: Local development environment configuration settings.
            repo_root: Optional repository root path for executing Bazel commands.
        """
        self.config = config
        self.repo_root = repo_root or _REPO_ROOT

    def get_service_specs(
        self,
        services: list[str] | None = None,
    ) -> list[tuple[str, str, str]]:
        """Resolves Bazel package, tarball target, and image tag specifications.

        Args:
            services: Optional list of service names. Defaults to config.enabled_services.

        Returns:
            List of tuples: (bazel_package, bazel_target, docker_image_tag).
        """
        active_services = services or self.config.enabled_services
        specs: list[tuple[str, str, str]] = []
        for s in active_services:
            if s in _SERVICE_TARGET_MAP:
                specs.append(_SERVICE_TARGET_MAP[s])
            else:
                log_info(f"Custom service '{s}' detected, using convention cmd/{s}...")
                specs.append((f"cmd/{s}", f"{s}_image_tarball", f"{s}_image:latest"))
        return specs

    def build_images(
        self,
        services: list[str] | None = None,
        skip_build: bool = False,
    ) -> list[str]:
        """Builds container images in parallel with Bazel and loads into Docker.

        Args:
            services: Optional list of specific services to build.
            skip_build: If True, skips compiling and assumes images exist in Docker.

        Returns:
            List of Docker image tags built and loaded.
        """
        specs = self.get_service_specs(services)
        image_tags = [tag for _, _, tag in specs]
        if skip_build:
            log_info("Skipping container image build (--skip-build specified).")
            return image_tags

        log_info(f"=== [Bazel] Building {len(specs)} container images in parallel ===")
        bazel_targets = [f"//{pkg}:{target}" for pkg, target, _ in specs]
        run_command(["bazel", "build", *bazel_targets], cwd=self.repo_root)

        bazel_bin_res = run_command(
            ["bazel", "info", "bazel-bin"],
            capture_output=True,
            cwd=self.repo_root,
        )
        bazel_bin = Path(bazel_bin_res.stdout.strip())

        log_info("Loading built image tarballs into Docker daemon...")
        for pkg, target, _ in specs:
            tar_path = bazel_bin / pkg / f"{target}.tar"
            run_command(["docker", "load", "-i", str(tar_path)])

        log_success(f"Successfully built and loaded {len(image_tags)} container images.")
        return image_tags

    def load_images_into_kind(self, image_tags: list[str]) -> None:
        """Loads container images from Docker into KinD cluster nodes.

        Args:
            image_tags: List of Docker image tags to load into KinD.
        """
        if not image_tags:
            return

        log_info(
            f"=== [KinD] Loading {len(image_tags)} container images into cluster "
            f"'{self.config.cluster_name}' ==="
        )
        run_command(
            [
                "kind",
                "load",
                "docker-image",
                *image_tags,
                "--name",
                self.config.cluster_name,
            ]
        )
        log_success("Container images loaded into KinD cluster.")
