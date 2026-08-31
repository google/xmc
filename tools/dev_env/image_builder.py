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

"""Parallel container image building via Bazel and Docker/KinD loading.

Note on OCI Image Build Architecture:
- `tools/common/build_oci.py`: Low-level tool invoked inside Bazel genrules (`//:oci.bzl`)
  to package a single cross-compiled Go binary into an OCI container `.tar` archive.
- `tools/dev_env/image_builder.py`: High-level dev orchestrator that executes a single
  concurrent `bazel build` across multiple service targets in parallel (which triggers
  `build_oci.py` under the hood), discovers output tarballs in `bazel-bin/`, loads them into
  the host Docker daemon, and injects them concurrently into KinD cluster nodes.
"""

from __future__ import annotations

import logging
from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig

_LOGGER = logging.getLogger("xmc.dev_env.image_builder")

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
    "ui": ("cmd/ui", "ui_image_tarball", "ui_image:latest"),
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

        Maps service identifiers to known package definitions or falls back to
        the standard naming convention (cmd/{service}:{service}_image_tarball).
        Deduplicates service names while preserving order and validates that
        service names contain only safe alphanumeric and dash/underscore characters.

        Args:
            services: Optional list of service names. If None, defaults to
                self.config.enabled_services.

        Returns:
            List of tuples: (bazel_package, bazel_target, docker_image_tag).

        Raises:
            ValueError: If any service name contains invalid or potentially unsafe characters.
        """
        active_services = self.config.enabled_services if services is None else services
        active_services = list(dict.fromkeys(active_services))
        specs: list[tuple[str, str, str]] = []
        for s in active_services:
            if not s or not all(c.isalnum() or c in "_-" for c in s):
                raise ValueError(
                    f"Invalid service name '{s}'. Service names must only contain "
                    "alphanumeric characters, underscores, or dashes."
                )
            if s in _SERVICE_TARGET_MAP:
                specs.append(_SERVICE_TARGET_MAP[s])
            else:
                log_info(f"Custom service '{s}' detected, using convention cmd/{s}...")
                specs.append((f"cmd/{s}", f"{s}_image_tarball", f"{s.lower()}_image:latest"))
        return specs

    def _resolve_tarball_path(self, bazel_bin: Path, pkg: str, target: str) -> Path:
        """Resolves path to the built image tarball under bazel-bin or bazel-out fallback.

        Args:
            bazel_bin: Path to resolved bazel-bin directory.
            pkg: Bazel package path relative to repo root (e.g. 'cmd/ui').
            target: Bazel tarball target name (e.g. 'ui_image_tarball').

        Returns:
            Path to the existing tarball file.

        Raises:
            FileNotFoundError: If the tarball cannot be found under bazel-bin or bazel-out.
        """
        tar_path = bazel_bin / pkg / f"{target}.tar"
        if tar_path.is_file():
            return tar_path

        # Fallback: search under bazel-out to handle platform transitions and cross-compilation
        bazel_out = self.repo_root / "bazel-out"
        if bazel_out.is_dir():
            candidates = list(bazel_out.glob(f"**/{pkg}/{target}.tar"))
            if candidates:
                return candidates[0]

        raise FileNotFoundError(
            f"Could not find built image tarball for {pkg}:{target}. "
            f"Expected at {tar_path} or under bazel-out/."
        )

    def build_images(
        self,
        services: list[str] | None = None,
        skip_build: bool = False,
    ) -> list[str]:
        """Builds container image tarballs in parallel via Bazel and loads them into Docker.

        Executes `bazel build //pkg:target` across all resolved targets in a single
        concurrent invocation to leverage Bazel's multi-core dependency graph.
        Locates generated `.tar` artifacts in the `bazel-bin` output tree and loads
        each into the local Docker daemon.

        Args:
            services: Optional list of specific services to build.
            skip_build: If True, bypasses Bazel compilation and returns expected image tags.

        Returns:
            List of Docker image tags built and loaded into the Docker daemon.

        Raises:
            RuntimeError: If bazel-bin directory resolution fails.
        """
        specs = self.get_service_specs(services)
        if not specs:
            log_info("No service images specified or enabled to build.")
            return []

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
        stdout = bazel_bin_res.stdout
        if isinstance(stdout, bytes):
            stdout = stdout.decode("utf-8")
        raw_bazel_bin = stdout.strip() if stdout else ""
        if not raw_bazel_bin:
            raise RuntimeError("Failed to resolve bazel-bin path from Bazel.")
        bazel_bin = Path(raw_bazel_bin)
        _LOGGER.debug("Resolved bazel-bin directory: %s", bazel_bin)

        log_info("Loading built image tarballs into Docker daemon in parallel...")
        from concurrent.futures import ThreadPoolExecutor

        def load_image(spec: tuple[str, str, str]) -> None:
            pkg, target, _ = spec
            tar_path = self._resolve_tarball_path(bazel_bin, pkg, target)
            _LOGGER.debug("Loading image tarball into Docker: %s", tar_path)
            run_command(["docker", "load", "-i", str(tar_path)], log_cmd=False)

        with ThreadPoolExecutor() as executor:
            list(executor.map(load_image, specs))

        log_success(f"Successfully built and loaded {len(image_tags)} container images.")
        return image_tags

    def load_images_into_kind(self, image_tags: list[str]) -> None:
        """Loads container images from the host Docker daemon directly into KinD cluster nodes.

        Uses `kind load docker-image` to transfer image layers into the containerd
        runtime of the KinD node containers, eliminating the need for an external OCI registry.

        Args:
            image_tags: List of Docker image tags to import into the KinD cluster.
        """
        if not image_tags:
            _LOGGER.debug("No container image tags provided to load into KinD.")
            return

        _LOGGER.debug(
            "Loading %d images into KinD cluster '%s': %s",
            len(image_tags),
            self.config.cluster_name,
            image_tags,
        )

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
