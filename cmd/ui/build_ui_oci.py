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

"""Script for compiling Angular UI application into an OCI container tarball.

This script implements a two-tier container build strategy for the Angular UI:

  1. Tier 1 - Fast Path (Pre-compiled bundle exists):
     When `test/e2e/lib/image_builder.py` pre-compiles the Angular bundle on the
     host before Bazel runs, `cmd/ui/dist/browser/index.html` is present in the
     source directory. The builder skips Node.js compilation entirely and packages
     the static assets directly into a minimal `nginx:alpine` image in 2-3 seconds.

  2. Tier 2 - Fallback Path (Containerized compilation):
     When no pre-compiled bundle is available (e.g. host lacks Node.js/npm or direct
     `bazel build //cmd/ui:ui_image_tarball` is invoked), the builder falls back to
     a self-contained multi-stage Docker build (`node:22-alpine` builder stage ->
     `nginx:alpine` runtime stage). Package files are copied first to leverage
     Docker layer caching across source-only edits.
"""

import hashlib
import os
import platform
import shutil
import subprocess
import sys
import tempfile


def get_sha256(filepath: str) -> str:
    """Computes SHA256 hex digest for a file.

    Args:
        filepath: Path to the file to hash.

    Returns:
        Hexadecimal SHA-256 string.
    """
    h = hashlib.sha256()
    with open(filepath, "rb") as f:
        while True:
            chunk = f.read(65536)
            if not chunk:
                break
            h.update(chunk)
    return h.hexdigest()


def translate_imports(content: str) -> str:
    """Translates legacy Angular import strings to npm package names.

    Args:
        content: Source code string from an Angular template, style, or TypeScript file.

    Returns:
        Translated source string compatible with standard npm dependencies.
    """
    content = content.replace(
        "'third_party/javascript/angular_components/material'",
        "'@angular/material'",
    )
    content = content.replace(
        '"third_party/javascript/angular_components/material"',
        '"@angular/material"',
    )
    content = content.replace("'third_party/xmanager_cloud/ui/consts'", "'consts'")
    content = content.replace('"third_party/xmanager_cloud/ui/consts"', '"consts"')
    content = content.replace("'third_party/xmanager_cloud/ui/custom_colors'", "'custom_colors'")
    content = content.replace('"third_party/xmanager_cloud/ui/custom_colors"', '"custom_colors"')
    content = content.replace(".css'", ".scss'")
    return content.replace('.css"', '.scss"')


def copy_flat(src_dir: str, dest_dir: str) -> None:
    """Copies UI source files resolving symlinks and translating legacy imports.

    Bazel sandboxes and monorepos often rely heavily on symlinks. When building
    in Docker, symlinks pointing outside the build context fail. This function
    recursively traverses `src_dir`, dereferences symlinks into flat physical files
    in `dest_dir`, and rewrites Google-internal/monorepo import paths into standard
    npm package names required by Angular CLI.

    Defensive safeguards:
      - Symlink cycles: Tracks canonical real paths (`os.path.realpath`) in `visited`
        to prevent infinite recursion.
      - Path traversal: Ensures relative paths do not escape `dest_dir` (`..` or absolute).
      - Build artifacts: Skips transient and non-source directories (`node_modules`,
        `dist`, `.angular`, `.git`, `BUILD.bazel`, python scripts).
    """
    if os.path.exists(dest_dir):
        shutil.rmtree(dest_dir)
    os.makedirs(dest_dir, exist_ok=True)

    ignored_dirs = {"build_tmp", "node_modules", ".angular", "dist", ".git", "__pycache__"}
    ignored_files = {"BUILD.bazel", "BUILD"}
    visited = set()
    for root, dirs, files in os.walk(src_dir, followlinks=True):
        # Guard against symlink cycles by tracking visited real filesystem paths.
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
            full_path = os.path.join(root, file)
            rel_path = os.path.relpath(full_path, src_dir)
            # Guard against path traversal if symlinks point outside src_dir.
            if rel_path.startswith("..") or os.path.isabs(rel_path):
                continue
            dest_path = os.path.join(dest_dir, rel_path)
            try:
                os.makedirs(os.path.dirname(dest_path), exist_ok=True)
                if file.endswith((".scss", ".ts", ".html")):
                    with open(full_path, encoding="utf-8", errors="ignore") as f:
                        content = f.read()

                    translated = translate_imports(content)

                    with open(dest_path, "w", encoding="utf-8") as f:
                        f.write(translated)
                else:
                    shutil.copy2(full_path, dest_path)
            except OSError as e:
                print(f"ERROR: Failed to copy {full_path} to {dest_path}: {e}", file=sys.stderr)
                raise


def main() -> None:
    """Main entrypoint for building UI container image.

    Strategy:
      1. Fast Path (Pre-compiled bundle exists):
         When `test/e2e/lib/image_builder.py` has already pre-compiled the UI on
         the host, `cmd/ui/dist/browser/index.html` is present. We bypass the heavy
         Node.js compilation container entirely and construct a lightweight,
         single-stage `nginx:alpine` image in 2-3 seconds.
      2. Fallback Path (Containerized compilation):
         If no pre-compiled bundle is found (e.g. host lacks Node.js, running direct
         `bazel build`), we perform a self-contained multi-stage Docker build
         (`node:22-alpine` -> `nginx:alpine`). Package files are copied first to
         maximize Docker layer caching across source-only changes.

    The resulting image is tagged as `ui-image:latest` and exported as a Docker
    tarball to the specified output path for KinD or Docker loading.
    """
    if len(sys.argv) < 2:
        print("Usage: build_ui_oci.py <output_tar>")
        sys.exit(1)

    output_tar = sys.argv[1]
    workspace_dir = os.getcwd()
    ui_src_dir = os.path.join(workspace_dir, "cmd/ui")

    # Detect if host pre-compilation occurred (index.html exists in dist/browser).
    # Note: Modification time staleness checks are performed on the host side in
    # `image_builder.py` prior to Bazel invocation, as Bazel sandbox normalizes mtimes.
    dist_browser_src = os.path.join(ui_src_dir, "dist", "browser")
    index_html = os.path.join(dist_browser_src, "index.html")
    has_prebuilt_dist = os.path.isdir(dist_browser_src) and os.path.isfile(index_html)

    # Use an isolated temporary directory for the Docker build context.
    # This prevents polluting the repository workspace and avoids concurrent write
    # conflicts during parallel Bazel actions.
    with tempfile.TemporaryDirectory() as temp_build_dir:
        dockerfile_path = os.path.join(temp_build_dir, "Dockerfile")

        if has_prebuilt_dist:
            # Fast Path: Static assets are already compiled. Copy dist/browser and Nginx config
            # into a lean single-stage container. Build time: ~2-3s.
            print(
                "Found pre-compiled Angular UI bundle. Using fast single-stage Nginx container"
                " build..."
            )
            dest_dist = os.path.join(temp_build_dir, "dist", "browser")
            shutil.copytree(dist_browser_src, dest_dist)
            shutil.copy2(os.path.join(ui_src_dir, "nginx.conf"), temp_build_dir)
            shutil.copy2(os.path.join(ui_src_dir, "generate_config.sh"), temp_build_dir)
            with open(dockerfile_path, "w") as f:
                f.write("""FROM nginx:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY dist/browser /usr/share/nginx/html
RUN chmod -R o+rX /usr/share/nginx/html
COPY generate_config.sh /docker-entrypoint.d/generate_config.sh
RUN chmod +x /docker-entrypoint.d/generate_config.sh
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
""")
        else:
            # Fallback Path: No pre-compiled bundle available. Run full multi-stage Docker build.
            # Copy package.json first so Docker can cache npm dependencies across source code edits.
            print(f"Preparing flat physical copy of UI codebase at {temp_build_dir}...")
            copy_flat(ui_src_dir, temp_build_dir)
            ui_build_config = os.environ.get("XMC_UI_BUILD_CONFIG", "development")
            if not ui_build_config or not all(c.isalnum() or c in "-_" for c in ui_build_config):
                raise ValueError(f"Invalid XMC_UI_BUILD_CONFIG: {ui_build_config}")
            with open(dockerfile_path, "w") as f:
                f.write(f"""FROM node:22-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --legacy-peer-deps
COPY . .
RUN npx ng build --configuration {ui_build_config}

FROM nginx:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist/browser /usr/share/nginx/html
RUN chmod -R o+rX /usr/share/nginx/html
COPY generate_config.sh /docker-entrypoint.d/generate_config.sh
RUN chmod +x /docker-entrypoint.d/generate_config.sh
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
""")

        tag = "ui-image:latest"
        print("Building Docker OCI frontend image...")
        build_cmd = ["docker", "build"]

        # Support cross-architecture building (e.g. ARM developer Mac targeting amd64 cluster).
        target_platform = os.environ.get("XMC_UI_PLATFORM")
        if not target_platform and platform.machine().lower() in (
            "arm64",
            "aarch64",
        ):
            print(
                "INFO: Building on an ARM host without XMC_UI_PLATFORM set. "
                "Defaulting target platform to 'linux/amd64' for deployment"
                " compatibility. To build for a different platform, set"
                " XMC_UI_PLATFORM (e.g. XMC_UI_PLATFORM=linux/arm64)."
            )
            target_platform = "linux/amd64"

        if target_platform:
            build_cmd.extend(["--platform", target_platform])

        build_cmd.extend(["-t", tag, temp_build_dir])

        try:
            subprocess.run(build_cmd, check=True)
        except subprocess.CalledProcessError as e:
            print(f"ERROR: Docker build failed: {e}")
            sys.exit(1)

        print(f"Saving Docker image to {output_tar}...")
        save_cmd = ["docker", "save", "-o", output_tar, tag]
        try:
            subprocess.run(save_cmd, check=True)
        except subprocess.CalledProcessError as e:
            print(f"ERROR: Docker save failed: {e}")
            sys.exit(1)

    print(f"Successfully built loadable OCI UI tarball: {output_tar}")


if __name__ == "__main__":
    main()
