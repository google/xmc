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

"""Build hooks for the xmc-metrics distribution.

Package metadata lives in pyproject.toml. This script only adds two setuptools
commands:

* build_py compiles api/protos/metrics/metrics.proto into xmc/_protos, using the
  grpcio-tools and protobuf versions pinned in [build-system].
* sdist stages that .proto into the archive, so the sdist builds outside the
  repository checkout.
"""

from __future__ import annotations

import shutil
import tempfile
from pathlib import Path

from setuptools import setup
from setuptools.command.build_py import build_py
from setuptools.command.sdist import sdist

METRICS_PROTO = Path("api", "protos", "metrics", "metrics.proto")
_GENERATED_SUFFIXES = ("_pb2.py", "_pb2.pyi")
_PROJECT_ROOT = Path(__file__).resolve().parent


def find_proto_root(project_root: Path) -> Path:
    """Returns the protoc include root that contains METRICS_PROTO.

    Args:
        project_root: Directory holding this setup.py.

    Returns:
        project_root when building from an sdist (the .proto is staged there), or
        the repository root (three levels up) when building from a checkout.

    Raises:
        FileNotFoundError: If neither location contains METRICS_PROTO.
    """
    candidates = [project_root]
    if len(project_root.parents) >= 3:
        candidates.append(project_root.parents[2])  # sdks/python/xmc -> repository root
    for candidate in candidates:
        if (candidate / METRICS_PROTO).is_file():
            return candidate
    raise FileNotFoundError(
        f"{METRICS_PROTO} not found in {project_root} or its repository root. "
        "Build xmc-metrics from a google/xmc checkout or from its sdist."
    )


def is_shipped_module(path: Path) -> bool:
    """Returns whether a module under the package directory belongs in the wheel."""
    return path.name != "setup.py" and not path.name.endswith("_test.py")


def compile_metrics_proto(proto_root: Path, out_dir: Path) -> None:
    """Compiles METRICS_PROTO into out_dir/metrics_pb2.py and metrics_pb2.pyi.

    Compiling with -I proto_root keeps the descriptor name
    "api/protos/metrics/metrics.proto", the name Bazel and the example images use,
    so identical copies can be loaded in one process.

    Args:
        proto_root: Include root returned by find_proto_root.
        out_dir: Destination package directory (xmc/_protos).

    Raises:
        RuntimeError: If protoc fails.
    """
    from grpc_tools import protoc  # Build-time only; pinned in [build-system].

    with tempfile.TemporaryDirectory() as tmp:
        args = [
            "grpc_tools.protoc",
            f"-I{proto_root}",
            f"--python_out={tmp}",
            f"--pyi_out={tmp}",
            str(proto_root / METRICS_PROTO),
        ]
        if protoc.main(args) != 0:
            raise RuntimeError(f"protoc failed to compile {proto_root / METRICS_PROTO}")
        generated_dir = Path(tmp) / METRICS_PROTO.parent
        out_dir.mkdir(parents=True, exist_ok=True)
        for suffix in _GENERATED_SUFFIXES:
            name = f"{METRICS_PROTO.stem}{suffix}"
            shutil.copyfile(generated_dir / name, out_dir / name)


# build_py and sdist are Any when the type-checking environment lacks setuptools;
# unused-ignore keeps the comment valid where setuptools is installed.
class BuildPyWithMetricsProto(build_py):  # type: ignore[misc, unused-ignore]
    """build_py that also generates xmc/_protos/metrics_pb2.py."""

    def run(self) -> None:
        """Copies the package sources, then compiles the metrics proto."""
        super().run()
        compile_metrics_proto(find_proto_root(_PROJECT_ROOT), self.protos_out_dir())

    def find_package_modules(self, package: str, package_dir: str) -> list[tuple[str, str, str]]:
        """Lists package modules except this build script and the unit tests.

        The project root doubles as the xmc package directory, so without this filter
        setup.py and every *_test.py would ship in the wheel.
        """
        modules = super().find_package_modules(package, package_dir)
        return [module for module in modules if is_shipped_module(Path(module[2]))]

    def protos_out_dir(self) -> Path:
        """Returns where the generated module goes.

        Editable installs import xmc straight from the source tree, so the module is
        generated in place (git-ignored); regular builds write into build_lib.
        """
        if self.editable_mode:
            return _PROJECT_ROOT / "_protos"
        return Path(self.build_lib) / "xmc" / "_protos"


class SdistWithMetricsProto(sdist):  # type: ignore[misc, unused-ignore]
    """sdist that stages METRICS_PROTO so the archive builds on its own."""

    def make_release_tree(self, base_dir: str, files: list[str]) -> None:
        """Builds the release tree, then copies METRICS_PROTO into it."""
        super().make_release_tree(base_dir, files)
        staged = Path(base_dir) / METRICS_PROTO
        staged.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(find_proto_root(_PROJECT_ROOT) / METRICS_PROTO, staged)


if __name__ == "__main__":
    setup(cmdclass={"build_py": BuildPyWithMetricsProto, "sdist": SdistWithMetricsProto})
