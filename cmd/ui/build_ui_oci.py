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

"""Script for compiling Angular UI application into an OCI container tarball."""

import hashlib
import os
import platform
import shutil
import subprocess
import sys
import tempfile


def get_sha256(filepath: str) -> str:
    """Computes SHA256 hex digest for a file."""
    h = hashlib.sha256()
    with open(filepath, "rb") as f:
        while True:
            chunk = f.read(65536)
            if not chunk:
                break
            h.update(chunk)
    return h.hexdigest()


def translate_imports(content: str) -> str:
    """Translates legacy Angular import strings to npm package name."""
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


def copy_flat(src_dir: str, dest_dir: str):
    """Copies files resolving symlinks and translates legacy path imports."""
    if os.path.exists(dest_dir):
        shutil.rmtree(dest_dir)
    os.makedirs(dest_dir, exist_ok=True)

    ignored_dirs = {"build_tmp", "node_modules", ".angular", "dist", ".git"}
    for root, dirs, files in os.walk(src_dir, followlinks=True):
        dirs[:] = [d for d in dirs if d not in ignored_dirs]
        for file in files:
            full_path = os.path.join(root, file)
            rel_path = os.path.relpath(full_path, src_dir)
            dest_path = os.path.join(dest_dir, rel_path)
            os.makedirs(os.path.dirname(dest_path), exist_ok=True)

            if file.endswith((".scss", ".ts", ".html")):
                with open(full_path, encoding="utf-8", errors="ignore") as f:
                    content = f.read()

                translated = translate_imports(content)

                with open(dest_path, "w", encoding="utf-8") as f:
                    f.write(translated)
            else:
                shutil.copy2(full_path, dest_path)


def main():
    """Main entrypoint for building UI container image."""
    if len(sys.argv) < 2:
        print("Usage: build_ui_oci.py <output_tar>")
        sys.exit(1)

    output_tar = sys.argv[1]
    workspace_dir = os.getcwd()
    ui_src_dir = os.path.join(workspace_dir, "cmd/ui")

    dist_browser_src = os.path.join(ui_src_dir, "dist", "browser")
    has_prebuilt_dist = os.path.isdir(dist_browser_src) and os.path.isfile(
        os.path.join(dist_browser_src, "index.html")
    )

    with tempfile.TemporaryDirectory() as temp_build_dir:
        dockerfile_path = os.path.join(temp_build_dir, "Dockerfile")

        if has_prebuilt_dist:
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
            print(f"Preparing flat physical copy of UI codebase at {temp_build_dir}...")
            copy_flat(ui_src_dir, temp_build_dir)
            with open(dockerfile_path, "w") as f:
                f.write("""FROM node:22-alpine AS builder
WORKDIR /app
COPY . .
RUN npm ci --legacy-peer-deps && npx ng build --configuration production

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
