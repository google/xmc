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

import sys
import os
import platform
import shutil
import subprocess
import tarfile
import hashlib
import json

def get_sha256(filepath):
    h = hashlib.sha256()
    with open(filepath, 'rb') as f:
        while True:
            chunk = f.read(65536)
            if not chunk:
                break
            h.update(chunk)
    return h.hexdigest()

def translate_imports(content):
    # Replace legacy Material imports with npm package name
    content = content.replace("'third_party/javascript/angular_components/material'", "'@angular/material'")
    content = content.replace('"third_party/javascript/angular_components/material"', '"@angular/material"')
    # Replace legacy path style consts/custom_colors with root load paths
    content = content.replace("'third_party/xmanager_cloud/ui/consts'", "'consts'")
    content = content.replace('"third_party/xmanager_cloud/ui/consts"', '"consts"')
    content = content.replace("'third_party/xmanager_cloud/ui/custom_colors'", "'custom_colors'")
    content = content.replace('"third_party/xmanager_cloud/ui/custom_colors"', '"custom_colors"')
    # Translate virtual .css styleUrl extensions to physical .scss files
    content = content.replace(".css'", ".scss'")
    content = content.replace('.css"', '.scss"')
    return content

def copy_flat(src_dir, dest_dir):
    """Copies files resolving symlinks and translates legacy paths into npm package imports."""
    if os.path.exists(dest_dir):
        shutil.rmtree(dest_dir)
    os.makedirs(dest_dir, exist_ok=True)
    
    # MUST set followlinks=True so os.walk traverses Bazel's symlinked directory structures!
    for root, _, files in os.walk(src_dir, followlinks=True):
        # Skip copying build_tmp itself to avoid infinite loops!
        if "build_tmp" in root:
            continue
        for file in files:
            full_path = os.path.join(root, file)
            rel_path = os.path.relpath(full_path, src_dir)
            dest_path = os.path.join(dest_dir, rel_path)
            os.makedirs(os.path.dirname(dest_path), exist_ok=True)
            
            # Check if the file is a stylesheet or source code that needs translation
            if file.endswith((".scss", ".ts", ".html")):
                with open(full_path, "r", encoding="utf-8", errors="ignore") as f:
                    content = f.read()
                
                translated = translate_imports(content)
                
                with open(dest_path, "w", encoding="utf-8") as f:
                    f.write(translated)
            else:
                # Otherwise, copy physically
                shutil.copy2(full_path, dest_path)

def main():
    if len(sys.argv) < 2:
        print("Usage: build_ui_oci.py <output_tar>")
        sys.exit(1)

    output_tar = sys.argv[1]
    workspace_dir = os.getcwd()
    ui_src_dir = os.path.join(workspace_dir, "cmd/ui")
    
    # Use workspace-nested build path to ensure Docker-in-VM volume sharing works
    temp_build_dir = os.path.join(ui_src_dir, "build_tmp")
    print(f"Preparing flat physical copy of UI codebase at {temp_build_dir}...")
    copy_flat(ui_src_dir, temp_build_dir)

    # 1. Write a simple multi-stage Dockerfile to temp_build_dir
    dockerfile_path = os.path.join(temp_build_dir, "Dockerfile")
    with open(dockerfile_path, "w") as f:
        f.write("""FROM node:22-alpine AS builder
WORKDIR /app
COPY . .
RUN npm ci --legacy-peer-deps && npx ng build --configuration production

FROM nginx:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist/browser /usr/share/nginx/html
COPY generate_config.sh /docker-entrypoint.d/generate_config.sh
RUN chmod +x /docker-entrypoint.d/generate_config.sh
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
""")

    # 2. Build the image using the Docker daemon
    tag = "ui-image:latest"
    print("Building Docker OCI frontend image...")
    build_cmd = ["docker", "build", "--no-cache"]
    # Optional target platform override (e.g. XMC_UI_PLATFORM=linux/amd64).
    # Without it, docker builds for the host architecture - which on ARM
    # hosts (Apple Silicon) silently produces an image that won't run on
    # amd64 clusters, so warn in that case.
    target_platform = os.environ.get("XMC_UI_PLATFORM")
    if not target_platform and platform.machine().lower() in ("arm64", "aarch64"):
        print(
            "INFO: Building on an ARM host without XMC_UI_PLATFORM set. "
            "Defaulting target platform to 'linux/amd64' for deployment compatibility. "
            "To build for a different platform, set XMC_UI_PLATFORM (e.g. XMC_UI_PLATFORM=linux/arm64)."
        )
        target_platform = "linux/amd64"

    if target_platform:
        build_cmd.extend(["--platform", target_platform])

    build_cmd.extend(["-t", tag, temp_build_dir])
    
    try:
        subprocess.run(build_cmd, check=True)
    except subprocess.CalledProcessError as e:
        print(f"ERROR: Docker build failed: {e}")
        try:
            shutil.rmtree(temp_build_dir)
        except:
            pass
        sys.exit(1)

    # 3. Save the image to output_tar
    print(f"Saving Docker image to {output_tar}...")
    save_cmd = ["docker", "save", "-o", output_tar, tag]
    try:
        subprocess.run(save_cmd, check=True)
    except subprocess.CalledProcessError as e:
        print(f"ERROR: Docker save failed: {e}")
        sys.exit(1)

    # 4. Clean up the intermediate temp directory
    try:
        shutil.rmtree(temp_build_dir)
    except Exception as e:
        print(f"Warning: Cleanup of {temp_build_dir} failed: {e}")

    print(f"Successfully built loadable OCI UI tarball: {output_tar}")

if __name__ == "__main__":
    main()
