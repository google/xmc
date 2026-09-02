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

    def _is_ui_dist_stale(self, ui_dir: str, index_html: str) -> bool:
        """Returns True if any UI source file is newer than the built index.html."""
        try:
            dist_mtime = os.path.getmtime(index_html)
        except OSError:
            return True

        ignored_dirs = {"node_modules", ".angular", "dist", ".git", "build_tmp", "__pycache__"}
        ignored_files = {"BUILD.bazel", "BUILD"}
        visited = set()
        for root, dirs, files in os.walk(ui_dir, followlinks=True):
            real_root = os.path.realpath(root)
            if real_root in visited:
                dirs[:] = []
                continue
            visited.add(real_root)
            dirs[:] = [d for d in dirs if d not in ignored_dirs]
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

    def _maybe_precompile_ui(self) -> None:
        """Pre-compiles Angular UI on host if Node.js and npm are available."""
        if not (shutil.which("node") and shutil.which("npm") and shutil.which("npx")):
            return

        ui_dir = os.path.join(self.cfg.workspace_root, "cmd", "ui")
        dist_dir = os.path.join(ui_dir, "dist", "browser")
        index_html = os.path.join(dist_dir, "index.html")

        if os.path.isfile(index_html):
            if not self._is_ui_dist_stale(ui_dir, index_html):
                logger.info(
                    "=== [UI] Pre-compiled Angular UI bundle already exists and is up-to-date ==="
                )
                return
            logger.info("=== [UI] UI source files modified since last build. Recompiling... ===")

        logger.info("=== [UI] Compiling Angular UI on host with local node/npm ===")
        node_modules_dir = os.path.join(ui_dir, "node_modules")
        node_modules_lock = os.path.join(node_modules_dir, ".package-lock.json")
        package_json = os.path.join(ui_dir, "package.json")
        package_lock = os.path.join(ui_dir, "package-lock.json")
        needs_install = not os.path.isfile(node_modules_lock)
        if not needs_install:
            try:
                installed_time = os.path.getmtime(node_modules_lock)
                if any(
                    os.path.isfile(p) and os.path.getmtime(p) > installed_time
                    for p in (package_lock, package_json)
                ):
                    needs_install = True
            except OSError:
                needs_install = True

        try:
            if needs_install:
                logger.info("=== [UI] Installing npm dependencies... ===")
                run_cmd(["npm", "ci", "--legacy-peer-deps"], cwd=ui_dir)
            run_cmd(["npx", "ng", "build", "--configuration", "production"], cwd=ui_dir)
            logger.info("=== [UI] Successfully pre-compiled Angular UI on host ===")
        except Exception as e:
            logger.warning(
                "[UI] Host compilation failed (%s). Falling back to containerized build...", e
            )
            shutil.rmtree(os.path.join(ui_dir, "dist"), ignore_errors=True)

    def build_all(self) -> list[str]:
        """Builds all backend and frontend images and required test binaries."""
        if self.cfg.skip_build:
            logger.info("=== [Bazel] Skipping image builds (--skip-build set) ===")
            return [tag for _, _, tag in self._SERVICES]

        self._maybe_precompile_ui()

        logger.info("=== [Bazel] Building OCI images in parallel ===")
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
