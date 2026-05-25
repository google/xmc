import sys
import os
import tarfile
import hashlib
import json
import time

def get_sha256(filepath):
    h = hashlib.sha256()
    with open(filepath, 'rb') as f:
        while True:
            chunk = f.read(65536)
            if not chunk:
                break
            h.update(chunk)
    return h.hexdigest()

def main():
    if len(sys.argv) < 5:
        print("Usage: build_oci.py <binary_path> <binary_name> <image_tag> <output_tar>")
        sys.exit(1)

    binary_path = sys.argv[1]
    binary_name = sys.argv[2]
    image_tag = sys.argv[3]
    output_tar = sys.argv[4]

    # 1. Create the layer tarball containing /app/<binary_name>
    layer_tar_path = "layer.tar"
    real_binary_path = os.path.realpath(binary_path)
    with tarfile.open(layer_tar_path, "w") as tar:
        # Create tar entry with specific permission and path
        info = tar.gettarinfo(real_binary_path, arcname=f"app/{binary_name}")
        info.uid = 0
        info.gid = 0
        info.uname = "root"
        info.gname = "root"
        info.mode = 0o755
        with open(real_binary_path, "rb") as f:
            tar.addfile(info, f)

    layer_sha = get_sha256(layer_tar_path)

    # 2. Create the config JSON file
    config = {
        "config": {
            "Entrypoint": [f"/app/{binary_name}"],
            "Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
        },
        "created": "1970-01-01T00:00:00Z",
        "architecture": "amd64",
        "os": "linux",
        "rootfs": {
            "type": "layers",
            "diff_ids": [f"sha256:{layer_sha}"]
        }
    }

    config_json_path = "config.json"
    with open(config_json_path, "w") as f:
        json.dump(config, f)

    config_sha = get_sha256(config_json_path)
    final_config_name = f"{config_sha}.json"
    os.rename(config_json_path, final_config_name)

    # 3. Create the manifest JSON file
    manifest = [
        {
            "Config": final_config_name,
            "RepoTags": [image_tag],
            "Layers": [layer_tar_path]
        }
    ]

    manifest_json_path = "manifest.json"
    with open(manifest_json_path, "w") as f:
        json.dump(manifest, f)

    # 4. Pack everything into the final output loadable tarball!
    with tarfile.open(output_tar, "w") as tar:
        tar.add(layer_tar_path, arcname=layer_tar_path)
        tar.add(final_config_name, arcname=final_config_name)
        tar.add(manifest_json_path, arcname=manifest_json_path)

    # Clean up intermediate files
    os.remove(layer_tar_path)
    os.remove(final_config_name)
    os.remove(manifest_json_path)

    print(f"Successfully built loadable OCI tarball: {output_tar}")

if __name__ == "__main__":
    main()
