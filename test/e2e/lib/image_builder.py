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
import shutil
from typing import ClassVar

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.controller_installer import CONTROLLER_RELEASES

logger = logging.getLogger("xmc_e2e_runner")


def _get_service_specs() -> list[tuple[str, str, str]]:
    """Generates Bazel package, target, and image tag specifications."""
    standard_services = [
        "experiment_state_server",
        "xid_service",
        "actuator",
        "reconciler",
        "xmc_bridge",
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

    _SERVICES: ClassVar[list[tuple[str, str, str]]] = _get_service_specs()
    _EXTRA_BUILD_TARGETS: ClassVar[list[str]] = [
        "//api/protos/experiment:experiment_messages_descriptor",
        "//api/protos/xid:xid_messages_descriptor",
        "//internal/storage/spanner/migrations:migrations",
        *(f"//test/e2e/manifests:{release.manifest_target}" for release in CONTROLLER_RELEASES),
    ]

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def _is_ui_dist_stale(self, ui_dir: str, index_html: str) -> bool:
        """Determines whether the pre-compiled UI bundle is stale compared to source files.

        Compares the modification time (`mtime`) of `dist/browser/index.html` against
        all frontend source files (`.ts`, `.html`, `.scss`, etc.) in `ui_dir`.

        Exclusions & Guards:
          - Ignores Bazel metadata (`BUILD.bazel`), Python helper scripts, and artifact
            directories (`node_modules`, `dist`, `.angular`, `.git`, `build_tmp`).
          - Tracks visited directories via `os.path.realpath` to safely handle circular
            symlinks without infinite recursion.
          - Returns True (stale) if `index.html` is missing, unreadable, or if any source
            file is newer than `index.html`.
        """
        try:
            dist_mtime = os.path.getmtime(index_html)
        except OSError:
            return True

        ignored_dirs = {"node_modules", ".angular", "dist", ".git", "build_tmp", "__pycache__"}
        ignored_files = {"BUILD.bazel", "BUILD"}
        visited = set()
        for root, dirs, files in os.walk(ui_dir, followlinks=True):
            # Guard against circular symlink loops.
            real_root = os.path.realpath(root)
            if real_root in visited:
                dirs[:] = []
                continue
            visited.add(real_root)
            filtered_dirs = []
            for d in dirs:
                if d in ignored_dirs:
                    continue
                real_dir_path = os.path.realpath(os.path.join(root, d))
                if any(ignored in real_dir_path.split(os.sep) for ignored in ignored_dirs):
                    continue
                filtered_dirs.append(d)
            dirs[:] = filtered_dirs
            for file in files:
                if file.endswith(".py") or file in ignored_files:
                    continue
                file_path = os.path.join(root, file)
                try:
                    if os.path.getmtime(file_path) > dist_mtime:
                        return True
                except OSError:
                    continue
        return False

    def _needs_npm_install(self, install_sentinel: str, ui_dir: str) -> bool:
        """Checks whether UI npm dependencies are missing or older than package manifests."""
        if not os.path.isfile(install_sentinel):
            return True
        try:
            installed_time = os.path.getmtime(install_sentinel)
            return any(
                os.path.isfile(p) and os.path.getmtime(p) > installed_time
                for p in (
                    os.path.join(ui_dir, "package-lock.json"),
                    os.path.join(ui_dir, "package.json"),
                )
            )
        except OSError:
            return True

    def _run_host_ui_build(self, ui_dir: str, node_modules_dir: str, install_sentinel: str) -> None:
        """Installs UI dependencies if stale and compiles the production Angular bundle."""
        if self._needs_npm_install(install_sentinel, ui_dir):
            logger.info("=== [UI] Installing npm dependencies... ===")
            run_cmd(["npm", "ci", "--legacy-peer-deps"], cwd=ui_dir)
            try:
                os.makedirs(node_modules_dir, exist_ok=True)
                with open(install_sentinel, "w", encoding="utf-8") as f:
                    f.write("ok")
            except OSError as e:
                logger.warning(
                    "[UI] Failed to write npm install sentinel %s: %s", install_sentinel, e
                )

        run_cmd(["npx", "ng", "build", "--configuration", "production"], cwd=ui_dir)
        logger.info("=== [UI] Successfully pre-compiled Angular UI on host ===")

    def _maybe_precompile_ui(self) -> None:
        """Pre-compiles the Angular UI on the host machine to accelerate OCI packaging."""
        ui_dir = os.path.join(self.cfg.workspace_root, "cmd", "ui")
        index_html = os.path.join(ui_dir, "dist", "browser", "index.html")

        # 1. Check if existing pre-compiled bundle exists and verify freshness.
        if os.path.isfile(index_html):
            if not self._is_ui_dist_stale(ui_dir, index_html):
                logger.info(
                    "=== [UI] Pre-compiled Angular UI bundle already exists and is up-to-date ==="
                )
                return
            logger.info("=== [UI] UI source files modified since last build. Recompiling... ===")
            try:
                shutil.rmtree(os.path.join(ui_dir, "dist"))
            except FileNotFoundError:
                pass
            except OSError as e:
                logger.warning("[UI] Failed to remove stale dist directory: %s", e)

        # 2. Skip host compilation if Node.js toolchain is not available on host.
        if not (shutil.which("node") and shutil.which("npm") and shutil.which("npx")):
            logger.info(
                "=== [UI] Node.js toolchain not found on host. Falling back to containerized"
                " build ==="
            )
            return

        logger.info("=== [UI] Compiling Angular UI on host with local node/npm ===")
        node_modules_dir = os.path.join(ui_dir, "node_modules")
        install_sentinel = os.path.join(node_modules_dir, ".install-success")
        try:
            self._run_host_ui_build(ui_dir, node_modules_dir, install_sentinel)
        except Exception as e:
            # 5. Clean up failed dist directory to force clean containerized Docker fallback.
            logger.warning(
                "[UI] Host compilation failed (%s). Falling back to containerized build...", e
            )
            try:
                shutil.rmtree(os.path.join(ui_dir, "dist"))
            except FileNotFoundError:
                pass
            except OSError as e:
                logger.warning("[UI] Failed to clean up failed dist directory: %s", e)

    def build_all(self) -> list[str]:
        """Builds all backend and frontend images and loads them into Docker.

        Execution flow:
          1. Host Pre-compilation: Triggers `_maybe_precompile_ui()` to build the
             Angular bundle on the host if Node.js is present.
          2. Parallel Bazel Build: Invokes `bazel build` on all backend service
             targets and `//cmd/ui:ui_image_tarball` concurrently. If the UI was
             pre-compiled, `ui_image_tarball` builds as a fast 2-second Nginx image.
          3. Docker Ingestion: Queries `bazel-bin` and loads all generated OCI tarballs
             into the local Docker daemon via `docker load`.
        """
        if self.cfg.skip_build:
            logger.info("=== [Bazel] Skipping image builds (--skip-build set) ===")
            return [tag for _, _, tag in self._SERVICES]

        # Step 1: Pre-compile Angular UI bundle on the host (if Node.js is present).
        self._maybe_precompile_ui()

        # Step 2: Build all backend services, prerequisites, and UI OCI image tarballs in parallel.
        logger.info("=== [Bazel] Building OCI images and test prerequisites in parallel ===")
        bazel_targets = [
            f"//{pkg}:{target}" for pkg, target, _ in self._SERVICES
        ] + self._EXTRA_BUILD_TARGETS
        run_cmd(["bazel", "build", *bazel_targets], cwd=self.cfg.workspace_root)

        bazel_bin_res = run_cmd(
            ["bazel", "info", "bazel-bin"], cwd=self.cfg.workspace_root, capture_output=True
        )
        bazel_bin = bazel_bin_res.stdout.strip()

        # Step 3: Load all produced OCI tarballs into Docker daemon.
        images: list[str] = []
        for pkg, target, tag in self._SERVICES:
            tar_path = os.path.join(bazel_bin, pkg, f"{target}.tar")
            run_cmd(["docker", "load", "-i", tar_path])
            images.append(tag)

        return images
