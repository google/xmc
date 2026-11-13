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
import os
import struct
import sys
import tarfile
import tempfile
from pathlib import Path

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


def detect_elf_architecture(binary_path: str):
    """Detects architecture of an ELF binary. Returns 'amd64', 'arm64', or None."""
    try:
        with open(binary_path, "rb") as f:
            header = f.read(64)
            if len(header) < 52:
                return None
            # Check ELF magic bytes: \x7f ELF
            if header[0:4] != b"\x7fELF":
                return None

            # e_machine is at offset 18 (2 bytes)
            # Go binaries for linux are almost always little-endian (class 2, data 1).
            ei_data = header[5]
            if ei_data == 1:  # Little endian
                (e_machine,) = struct.unpack("<H", header[18:20])
            elif ei_data == 2:  # Big endian
                (e_machine,) = struct.unpack(">H", header[18:20])
            else:
                return None

            if e_machine == 62:  # EM_X86_64
                return "amd64"
            if e_machine == 183:  # EM_AARCH64
                return "arm64"
            if e_machine == 3:  # EM_386
                return "386"
            if e_machine == 40:  # EM_ARM
                return "arm"
    except Exception:
        pass
    return None


def extract_migration_version(migration_files: list[str] | None) -> int:
    """Extracts the highest numeric migration version from SQL file paths.

    Args:
        migration_files: Optional list of SQL migration file paths.

    Returns:
        Highest integer prefix before '_' in the filenames, or 0 if none.
    """
    if not migration_files:
        return 0
    return max(
        (
            int(prefix)
            for path in migration_files
            if (prefix := Path(path).name.partition("_")[0]).isdigit()
        ),
        default=0,
    )


def _resolve_version_arg(explicit_version: object, migration_files: object) -> int:
    """Resolves a positive integer schema version from an explicit value or file list.

    Args:
        explicit_version: Explicit version integer or numeric string, if provided.
        migration_files: Sequence of migration file path strings, if provided.

    Returns:
        Resolved schema version integer, or 0 if neither is provided.
    """
    if isinstance(explicit_version, int) and not isinstance(explicit_version, bool):
        return max(explicit_version, 0)
    if isinstance(explicit_version, str) and explicit_version.isdigit():
        return int(explicit_version)
    if isinstance(migration_files, (list, tuple)):
        return extract_migration_version([str(p) for p in migration_files])
    return 0


def build_schema_labels(args: argparse.Namespace) -> dict[str, str]:
    """Builds OCI image schema version labels from CLI arguments.

    Args:
        args: Parsed CLI arguments containing optional migration paths or versions.

    Returns:
        Dictionary of OCI label keys to string schema versions.
    """
    labels: dict[str, str] = {}
    ess_ver = _resolve_version_arg(
        getattr(args, "ess_schema_version", None),
        getattr(args, "ess_migrations", None),
    )
    if ess_ver > 0:
        labels["xmc.google.com/ess-schema-version"] = str(ess_ver)

    xid_ver = _resolve_version_arg(
        getattr(args, "xid_schema_version", None),
        getattr(args, "xid_migrations", None),
    )
    if xid_ver > 0:
        labels["xmc.google.com/xid-schema-version"] = str(xid_ver)

    return labels


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

        try:
            with tarfile.open(layer_tar_path, "w") as tar:
                info = tar.gettarinfo(real_binary_path, arcname=f"app/{binary_name}")
                info.uid = 0
                info.gid = 0
                info.uname = "root"
                info.gname = "root"
                info.mode = EXEC_PERMISSION
                with open(real_binary_path, "rb") as f:
                    tar.addfile(info, f)

                if host_ca_path:
                    info_ca = tar.gettarinfo(
                        host_ca_path, arcname="etc/ssl/certs/ca-certificates.crt"
                    )
                    info_ca.uid = 0
                    info_ca.gid = 0
                    info_ca.uname = "root"
                    info_ca.gname = "root"
                    info_ca.mode = FILE_PERMISSION
                    with open(host_ca_path, "rb") as f:
                        tar.addfile(info_ca, f)
        except Exception as e:
            raise HandledError(
                f"Failed to create layer tarball: {e}",
                "Check directory write permissions and ensure the binary path is a valid file.",
            )

        layer_sha = get_sha256(layer_tar_path)
        labels = build_schema_labels(args)
        container_config: dict[str, object] = {
            "Entrypoint": [f"/app/{binary_name}"],
            "Env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"],
        }
        if labels:
            container_config["Labels"] = labels

        config = {
            "config": container_config,
            "created": "1970-01-01T00:00:00Z",
            "architecture": arch,
            "os": "linux",
            "rootfs": {"type": "layers", "diff_ids": [f"sha256:{layer_sha}"]},
        }

        config_json_path = os.path.join(tmpdir, "config.json")
        try:
            with open(config_json_path, "w") as f:
                json.dump(config, f)
        except Exception as e:
            raise HandledError(
                f"Failed to write config JSON: {e}",
                "Check disk space and write permissions in the current working directory.",
            )

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
        try:
            with open(manifest_json_path, "w") as f:
                json.dump(manifest, f)
        except Exception as e:
            raise HandledError(
                f"Failed to write manifest JSON: {e}",
                "Check disk space and write permissions in the current working directory.",
            )

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
            )

    return {
        "output_tar": output_tar,
        "image_tag": image_tag,
        "layer_sha256": layer_sha,
        "config_sha256": config_sha,
        "labels": labels,
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
    parser.add_argument(
        "--ess-migrations",
        nargs="*",
        default=None,
        help=(
            "Optional list of ESS SQL migration files used to compute"
            " xmc.google.com/ess-schema-version."
        ),
    )
    parser.add_argument(
        "--xid-migrations",
        nargs="*",
        default=None,
        help=(
            "Optional list of XID SQL migration files used to compute"
            " xmc.google.com/xid-schema-version."
        ),
    )
    parser.add_argument(
        "--ess-schema-version",
        type=int,
        default=None,
        help="Explicit ESS schema version to embed in OCI image labels.",
    )
    parser.add_argument(
        "--xid-schema-version",
        type=int,
        default=None,
        help="Explicit XID schema version to embed in OCI image labels.",
    )
    run_tool(parser, build_oci)


if __name__ == "__main__":
    main()
