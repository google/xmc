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

"""setuptools commands that stage protos and xdash into the xmanager-cloud build.

Project metadata lives in pyproject.toml; this file only wires the build_staging
functions into the sdist and build_py commands.
"""

import sys
import tempfile
from pathlib import Path

from setuptools import setup
from setuptools.command.build_py import build_py
from setuptools.command.sdist import sdist

_PROJECT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(_PROJECT_DIR))

import build_staging  # noqa: E402


# setuptools ships no type information, so its command classes are typed as Any.
class SdistWithStagedSources(sdist):  # type: ignore[misc]
    """Copies the protos and xdash sources into the sdist under `_staged/`."""

    def make_release_tree(self, base_dir: str, files: list[str]) -> None:
        super().make_release_tree(base_dir, files)
        layout = build_staging.resolve_source_layout(_PROJECT_DIR)
        build_staging.copy_files(build_staging.sdist_copy_plan(layout), Path(base_dir))


class BuildPyWithProtos(build_py):  # type: ignore[misc]
    """Compiles the XMC protos and bundles xdash into the build directory."""

    def find_package_modules(self, package: str, package_dir: str) -> list[tuple[str, str, str]]:
        modules = super().find_package_modules(package, package_dir)
        return [module for module in modules if not build_staging.is_test_module(module[1])]

    def run(self) -> None:
        super().run()
        # Resolve include paths first: it reports a missing grpcio-tools clearly.
        include_dirs = build_staging.protoc_include_dirs()
        from grpc_tools import protoc

        layout = build_staging.resolve_source_layout(_PROJECT_DIR)
        build_lib = Path(self.build_lib)
        build_staging.copy_files(build_staging.xdash_copy_plan(layout), build_lib)
        build_staging.write_init_files(build_lib)
        proto_plan = build_staging.proto_copy_plan(layout)
        with tempfile.TemporaryDirectory() as proto_tree:
            build_staging.copy_files(proto_plan, Path(proto_tree))
            build_staging.compile_protos(
                proto_tree=Path(proto_tree),
                proto_files=[relative for _, relative in proto_plan],
                include_dirs=include_dirs,
                output_dir=build_lib,
                protoc_main=protoc.main,
            )


setup(cmdclass={"sdist": SdistWithStagedSources, "build_py": BuildPyWithProtos})
