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

"""Stages XMC protos and the xdash SDK into the xmanager-cloud sdist and wheel.

`api/protos/` and `sdks/python/xdash/` live outside this project directory. The
sdist hook copies them under `_staged/` so the sdist builds on its own; the
build_py hook compiles the protos into the wheel's `xmanager_cloud` package and
copies the xdash sources next to it. Every function here uses only the standard
library so it stays testable without a build environment.
"""

from __future__ import annotations

import dataclasses
import importlib.util
import shutil
from collections.abc import Callable, Iterable, Sequence
from importlib.machinery import ModuleSpec
from pathlib import Path, PurePosixPath

STAGED_DIR_NAME = "_staged"

# (api/protos subdirectory, xmanager_cloud subpackage). The subpackage paths match
# the `import_prefix` of the Bazel proto_library targets and the `import` lines in
# the .proto files, so generated modules keep their existing import paths, e.g.
# `xmanager_cloud.experiment_state_server.proto.api_pb2`. Keep in sync with
# PROTO_PACKAGE_DIRS in deploy/cli/install_xmanager.py and the paths filter in
# .github/workflows/xmanager-cloud-package.yml.
PROTO_PACKAGES: tuple[tuple[str, str], ...] = (
    ("experiment", "experiment_state_server"),
    ("xid", "xid_service"),
    ("dashboard", "dashboard_service"),
)

# A copy plan: (source file, destination path relative to a target root).
CopyPlan = list[tuple[Path, PurePosixPath]]


@dataclasses.dataclass(frozen=True)
class SourceLayout:
    """Locations of the sources that live outside the project directory.

    Attributes:
        proto_root: Directory holding the `experiment/`, `xid/`, and `dashboard/`
            proto subdirectories (the repository's `api/protos/`).
        xdash_dir: Directory holding the xdash SDK sources.
    """

    proto_root: Path
    xdash_dir: Path


def resolve_source_layout(project_dir: Path) -> SourceLayout:
    """Finds the proto and xdash sources for a build.

    An unpacked sdist carries the sources under `_staged/`; a repository checkout
    reads them from `api/protos/` and `sdks/python/xdash/`.

    Args:
        project_dir: Directory containing this project's `pyproject.toml`.

    Returns:
        The source layout to build from.

    Raises:
        FileNotFoundError: If neither layout is present.
    """
    staged = project_dir / STAGED_DIR_NAME
    if staged.is_dir():
        return SourceLayout(proto_root=staged / "api" / "protos", xdash_dir=staged / "xdash")
    hint = "build xmanager-cloud from the XMC repository or its sdist."
    if len(project_dir.parents) < 3:
        raise FileNotFoundError(f"Found no {staged} (sdist) and no repository checkout; {hint}")
    repo = SourceLayout(
        proto_root=project_dir.parents[2] / "api" / "protos",
        xdash_dir=project_dir.parent / "xdash",
    )
    if repo.proto_root.is_dir() and repo.xdash_dir.is_dir():
        return repo
    raise FileNotFoundError(
        f"Found neither {staged} (sdist) nor {repo.proto_root} and {repo.xdash_dir} "
        f"(repository checkout); {hint}"
    )


def protoc_include_dirs(
    find_spec: Callable[[str], ModuleSpec | None] = importlib.util.find_spec,
) -> list[Path]:
    """Returns the well-known (grpc_tools) and googleapis proto include paths.

    Args:
        find_spec: `importlib.util.find_spec`, injected so tests need no build env.

    Returns:
        The `grpc_tools/_proto` directory and the root that contains `google/api/`.

    Raises:
        RuntimeError: If grpcio-tools or googleapis-common-protos is not installed.
    """
    grpc_tools = find_spec("grpc_tools")
    try:
        googleapis = find_spec("google.api")
    except ModuleNotFoundError:  # No `google` namespace package at all.
        googleapis = None
    if grpc_tools is None or grpc_tools.origin is None:
        raise RuntimeError("grpcio-tools must be a build dependency")
    if googleapis is None or not googleapis.submodule_search_locations:
        raise RuntimeError("googleapis-common-protos must be a build dependency")
    googleapis_root = Path(googleapis.submodule_search_locations[0]).parent.parent
    return [Path(grpc_tools.origin).parent / "_proto", googleapis_root]


def is_test_module(module_name: str) -> bool:
    """Reports whether a module is a unit test that must stay out of the wheel."""
    return module_name.endswith("_test")


def _proto_files(proto_root: Path) -> Iterable[tuple[str, str, Path]]:
    """Yields (proto subdirectory, xmanager_cloud subpackage, .proto file)."""
    for src_subdir, dst_package in PROTO_PACKAGES:
        src_dir = proto_root / src_subdir
        protos = sorted(src_dir.glob("*.proto"))
        if not protos:
            raise FileNotFoundError(f"No .proto files found in {src_dir}")
        for proto in protos:
            yield src_subdir, dst_package, proto


def _xdash_files(xdash_dir: Path) -> list[Path]:
    """Lists the xdash files shipped in the wheel: non-test modules and `py.typed`.

    xdash is a flat package today; a nested module fails the build instead of
    being silently left out of the copy plans.
    """
    modules = [path for path in xdash_dir.glob("*.py") if not is_test_module(path.stem)]
    if not modules:
        raise FileNotFoundError(f"No xdash modules found in {xdash_dir}")
    nested = sorted(path for path in xdash_dir.rglob("*.py") if path.parent != xdash_dir)
    if nested:
        raise RuntimeError(f"xdash subpackages are not staged yet: {nested[0]}")
    marker = xdash_dir / "py.typed"
    if not marker.is_file():
        raise FileNotFoundError(f"Missing {marker}")
    return sorted([*modules, marker])


def sdist_copy_plan(layout: SourceLayout) -> CopyPlan:
    """Plans the copies that make the sdist self-contained, under `_staged/`."""
    staged = PurePosixPath(STAGED_DIR_NAME)
    protos = [
        (proto, staged / "api" / "protos" / src_subdir / proto.name)
        for src_subdir, _, proto in _proto_files(layout.proto_root)
    ]
    xdash = [(path, staged / "xdash" / path.name) for path in _xdash_files(layout.xdash_dir)]
    return [*protos, *xdash]


def proto_copy_plan(layout: SourceLayout) -> CopyPlan:
    """Plans the .proto copies into the `xmanager_cloud/<package>/proto/` tree."""
    return [
        (proto, PurePosixPath("xmanager_cloud", dst_package, "proto", proto.name))
        for _, dst_package, proto in _proto_files(layout.proto_root)
    ]


def xdash_copy_plan(layout: SourceLayout) -> CopyPlan:
    """Plans the xdash copies into the wheel's top-level `xdash` package."""
    return [(path, PurePosixPath("xdash", path.name)) for path in _xdash_files(layout.xdash_dir)]


def generated_init_files() -> list[PurePosixPath]:
    """Lists the `__init__.py` files of the generated proto subpackages."""
    return [
        init
        for _, dst_package in PROTO_PACKAGES
        for init in (
            PurePosixPath("xmanager_cloud", dst_package, "__init__.py"),
            PurePosixPath("xmanager_cloud", dst_package, "proto", "__init__.py"),
        )
    ]


def write_init_files(target_root: Path) -> None:
    """Creates the empty `__init__.py` files of the generated proto subpackages."""
    for init_file in generated_init_files():
        (target_root / init_file).parent.mkdir(parents=True, exist_ok=True)
        (target_root / init_file).touch()


def expected_proto_modules(layout: SourceLayout) -> list[str]:
    """Lists the importable `*_pb2` and `*_pb2_grpc` modules the wheel must contain."""
    return [
        f"xmanager_cloud.{dst_package}.proto.{proto.stem}{suffix}"
        for _, dst_package, proto in _proto_files(layout.proto_root)
        for suffix in ("_pb2", "_pb2_grpc")
    ]


def copy_files(plan: CopyPlan, target_root: Path) -> None:
    """Copies every planned file under target_root, creating parent directories."""
    for source, relative in plan:
        destination = target_root / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination)


def compile_protos(
    proto_tree: Path,
    proto_files: Sequence[PurePosixPath],
    include_dirs: Sequence[Path],
    output_dir: Path,
    protoc_main: Callable[[list[str]], int],
) -> None:
    """Compiles staged .proto files into Python and gRPC modules.

    Args:
        proto_tree: Root holding the staged `xmanager_cloud/**/*.proto` files. It
            is the first include path, so descriptors keep the
            `xmanager_cloud/<package>/proto/<file>.proto` names that the .proto
            `import` lines use.
        proto_files: Staged .proto paths, relative to proto_tree.
        include_dirs: Extra include paths for well-known and googleapis protos.
        output_dir: Directory receiving the generated modules.
        protoc_main: `grpc_tools.protoc.main`, injected so tests need no protoc.

    Raises:
        RuntimeError: If protoc exits with a non-zero status.
    """
    args = [
        "grpc_tools.protoc",
        f"-I{proto_tree}",
        *(f"-I{include}" for include in include_dirs),
        f"--python_out={output_dir}",
        f"--grpc_python_out={output_dir}",
        *(str(proto_tree / proto) for proto in proto_files),
    ]
    status = protoc_main(args)
    if status != 0:
        raise RuntimeError(f"protoc failed with exit status {status}")
