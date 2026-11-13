# Copyright 2025 Google LLC
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

"""Tool for building OCI container image tarballs from compiled binaries."""

import argparse
import hashlib
import json
import logging
import os
import struct
import sys
import tarfile
import tempfile
from pathlib import Path

_LOGGER = logging.getLogger(__name__)

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

# Permission modes for files in the container layer.
# 0o755 represents owner read/write/execute, group/others read/execute.
EXEC_PERMISSION = 0o755
# 0o644 represents owner read/write, group/others read-only.
FILE_PERMISSION = 0o644


def get_sha256(filepath: str) -> str:
    """Computes the SHA256 checksum of a file in binary mode."""
    with open(filepath, "rb") as f:
        try:
            if hasattr(hashlib, "file_digest"):
                return hashlib.file_digest(f, "sha256").hexdigest()
        except (TypeError, ValueError):
            pass
        h = hashlib.sha256()
        while chunk := f.read(65536):
            h.update(chunk)
        return h.hexdigest()


_ELF_MACHINE_ARCH = {
    62: "amd64",  # EM_X86_64
    183: "arm64",  # EM_AARCH64
    3: "386",  # EM_386
    40: "arm",  # EM_ARM
}


def detect_elf_architecture(binary_path: str):
    """Detects architecture of an ELF binary. Returns 'amd64', 'arm64', or None."""
    try:
        with open(binary_path, "rb") as f:
            header = f.read(64)
            if len(header) < 52 or header[0:4] != b"\x7fELF":
                return None
            ei_data = header[5]
            if ei_data not in (1, 2):
                return None
            endian = "<H" if ei_data == 1 else ">H"
            (e_machine,) = struct.unpack(endian, header[18:20])
            return _ELF_MACHINE_ARCH.get(e_machine)
    except Exception as e:
        _LOGGER.debug("Failed to detect ELF architecture for %s: %s", binary_path, e)
    return None


def _add_root_file_to_tar(tar: tarfile.TarFile, src_path: str, arcname: str, mode: int) -> None:
    info = tar.gettarinfo(src_path, arcname=arcname)
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.mode = mode
    with open(src_path, "rb") as f:
        tar.addfile(info, f)


def _create_layer_tarball(
    layer_tar_path: str, real_binary_path: str, binary_name: str, host_ca_path: str | None
) -> None:
    try:
        with tarfile.open(layer_tar_path, "w") as tar:
            _add_root_file_to_tar(tar, real_binary_path, f"app/{binary_name}", EXEC_PERMISSION)
            if host_ca_path:
                _add_root_file_to_tar(
                    tar, host_ca_path, "etc/ssl/certs/ca-certificates.crt", FILE_PERMISSION
                )
    except Exception as e:
        raise HandledError(
            f"Failed to create layer tarball: {e}",
            "Check directory write permissions and ensure the binary path is a valid file.",
        ) from e


def _write_json_file(path: str, payload: object, label: str) -> None:
    try:
        with open(path, "w") as f:
            json.dump(payload, f)
    except Exception as e:
        raise HandledError(
            f"Failed to write {label} JSON: {e}",
            "Check disk space and write permissions in the current working directory.",
        ) from e


def build_oci(args):
    """Packages a binary into an OCI container image tarball."""
    binary_path = args.binary_path
    binary_name = args.binary_name
    image_tag = args.image_tag
    output_tar = args.output_tar

    if not os.path.exists(binary_path):
        raise HandledError(
            f"Binary path not found: {binary_path}",
            "Ensure that the binary is built and exists at the specified path before"
            " running build_oci.py.",
        )

    real_binary_path = os.path.realpath(binary_path)
    arch = detect_elf_architecture(real_binary_path)
    if not arch:
        raise HandledError(
            f"The binary at {binary_path} is not a valid Linux ELF file, or its"
            " architecture is unsupported.",
            "When building container images, please ensure the binary is"
            " cross-compiled for Linux (e.g. using"
            " --platforms=@io_bazel_rules_go//go/toolchain:linux_amd64).",
        )

    ca_paths = [
        "/etc/ssl/certs/ca-certificates.crt",
        "/etc/pki/tls/certs/ca-bundle.crt",
        "/etc/ssl/ca-bundle.pem",
        "/etc/ssl/cert.pem",
    ]
    host_ca_path = next((p for p in ca_paths if Path(p).is_file()), None)

    with tempfile.TemporaryDirectory() as tmpdir:
        layer_tar_path = os.path.join(tmpdir, "layer.tar")
        _create_layer_tarball(layer_tar_path, real_binary_path, binary_name, host_ca_path)
        layer_sha = get_sha256(layer_tar_path)

        config = {
            "config": {
                "Entrypoint": [f"/app/{binary_name}"],
                "Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],
            },
            "created": "1970-01-01T00:00:00Z",
            "architecture": arch,
            "os": "linux",
            "rootfs": {"type": "layers", "diff_ids": [f"sha256:{layer_sha}"]},
        }
        config_json_path = os.path.join(tmpdir, "config.json")
        _write_json_file(config_json_path, config, "config")
        config_sha = get_sha256(config_json_path)
        final_config_path = os.path.join(tmpdir, f"{config_sha}.json")
        os.rename(config_json_path, final_config_path)

        manifest = [
            {
                "Config": f"{config_sha}.json",
                "RepoTags": [image_tag],
                "Layers": ["layer.tar"],
            }
        ]
        manifest_json_path = os.path.join(tmpdir, "manifest.json")
        _write_json_file(manifest_json_path, manifest, "manifest")

        try:
            with tarfile.open(output_tar, "w") as tar:
                tar.add(layer_tar_path, arcname="layer.tar")
                tar.add(final_config_path, arcname=f"{config_sha}.json")
                tar.add(manifest_json_path, arcname="manifest.json")
        except Exception as e:
            raise HandledError(
                f"Failed to compile final OCI tarball: {e}",
                "Check permissions and ensure the target directory for output_tar"
                " exists and is writeable.",
            ) from e

    return {
        "output_tar": output_tar,
        "image_tag": image_tag,
        "layer_sha256": layer_sha,
        "config_sha256": config_sha,
    }


def main():
    """Main entrypoint parsing CLI arguments and executing OCI build."""
    parser = argparse.ArgumentParser(
        description=(
            "Build a loadable OCI container image tarball containing a binary and"
            " optional host CA certificates."
        )
    )
    parser.add_argument("binary_path", help="Path to the binary file to package.")
    parser.add_argument(
        "binary_name",
        help="Name of the binary inside the container (placed under /app/).",
    )
    parser.add_argument("image_tag", help="OCI image tag to apply (e.g. 'my-image:latest').")
    parser.add_argument("output_tar", help="Path where the output OCI tarball should be saved.")
    run_tool(parser, build_oci)


if __name__ == "__main__":
    main()
