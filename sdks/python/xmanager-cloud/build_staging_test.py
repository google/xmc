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

"""Unit tests for build_staging."""

import tempfile
import unittest
from collections.abc import Callable
from importlib.machinery import ModuleSpec
from pathlib import Path, PurePosixPath

import build_staging

# Baseline fixture: two protos per package, xdash modules plus a test module.
_PROTOS = {
    "experiment": ("api.proto", "experiment.proto"),
    "xid": ("api.proto", "messages.proto"),
    "dashboard": ("dashboard_service.proto", "messages.proto"),
}
_XDASH_FILES = ("__init__.py", "cli.py", "cli_test.py", "py.typed")


def _write(path: Path, content: str = "") -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


def _make_sources(root: Path) -> build_staging.SourceLayout:
    """Creates a proto tree and an xdash tree under root."""
    layout = build_staging.SourceLayout(proto_root=root / "protos", xdash_dir=root / "xdash")
    for subdir, names in _PROTOS.items():
        for name in names:
            _write(layout.proto_root / subdir / name, f"// {subdir}/{name}")
    for name in _XDASH_FILES:
        _write(layout.xdash_dir / name, name)
    return layout


class BuildStagingTestCase(unittest.TestCase):
    """Provides a temporary directory per test."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)


class ResolveSourceLayoutTest(BuildStagingTestCase):
    def _repo_project_dir(self) -> Path:
        project_dir = self.root / "sdks" / "python" / "xmanager-cloud"
        project_dir.mkdir(parents=True)
        return project_dir

    def test_repository_checkout(self) -> None:
        project_dir = self._repo_project_dir()
        (self.root / "api" / "protos").mkdir(parents=True)
        (self.root / "sdks" / "python" / "xdash").mkdir()

        layout = build_staging.resolve_source_layout(project_dir)

        self.assertEqual(layout.proto_root, self.root / "api" / "protos")
        self.assertEqual(layout.xdash_dir, self.root / "sdks" / "python" / "xdash")

    def test_staged_sdist_takes_precedence_over_repository(self) -> None:
        project_dir = self._repo_project_dir()
        (self.root / "api" / "protos").mkdir(parents=True)
        (self.root / "sdks" / "python" / "xdash").mkdir()
        (project_dir / "_staged").mkdir()

        layout = build_staging.resolve_source_layout(project_dir)

        self.assertEqual(layout.proto_root, project_dir / "_staged" / "api" / "protos")
        self.assertEqual(layout.xdash_dir, project_dir / "_staged" / "xdash")

    def test_missing_sources_raise(self) -> None:
        project_dir = self._repo_project_dir()
        (self.root / "api" / "protos").mkdir(parents=True)

        with self.assertRaisesRegex(FileNotFoundError, "Found neither"):
            build_staging.resolve_source_layout(project_dir)

    def test_shallow_project_dir_raises_file_not_found(self) -> None:
        with self.assertRaisesRegex(FileNotFoundError, "no repository checkout"):
            build_staging.resolve_source_layout(Path("/xc"))


class ProtocIncludeDirsTest(BuildStagingTestCase):
    def _find_spec(self, specs: dict[str, ModuleSpec | None]) -> Callable[[str], ModuleSpec | None]:
        return lambda name: specs[name]

    def _specs(self) -> dict[str, ModuleSpec | None]:
        grpc_tools = ModuleSpec(
            "grpc_tools", None, origin=str(self.root / "grpc_tools/__init__.py")
        )
        googleapis = ModuleSpec("google.api", None, is_package=True)
        googleapis.submodule_search_locations = [str(self.root / "site" / "google" / "api")]
        return {"grpc_tools": grpc_tools, "google.api": googleapis}

    def test_returns_well_known_and_googleapis_roots(self) -> None:
        include_dirs = build_staging.protoc_include_dirs(self._find_spec(self._specs()))

        self.assertEqual(include_dirs, [self.root / "grpc_tools" / "_proto", self.root / "site"])

    def test_missing_grpc_tools_raises(self) -> None:
        specs = {**self._specs(), "grpc_tools": None}

        with self.assertRaisesRegex(RuntimeError, "grpcio-tools"):
            build_staging.protoc_include_dirs(self._find_spec(specs))

    def test_missing_googleapis_raises(self) -> None:
        specs = {**self._specs(), "google.api": None}

        with self.assertRaisesRegex(RuntimeError, "googleapis-common-protos"):
            build_staging.protoc_include_dirs(self._find_spec(specs))

    def test_missing_google_namespace_raises(self) -> None:
        specs = self._specs()

        def find_spec(name: str) -> ModuleSpec | None:
            if name == "google.api":
                raise ModuleNotFoundError("No module named 'google'")
            return specs[name]

        with self.assertRaisesRegex(RuntimeError, "googleapis-common-protos"):
            build_staging.protoc_include_dirs(find_spec)


class CopyPlanTest(BuildStagingTestCase):
    def test_sdist_plan_stages_protos_and_shipped_xdash_files(self) -> None:
        layout = _make_sources(self.root)

        plan = build_staging.sdist_copy_plan(layout)

        self.assertCountEqual(
            [str(dst) for _, dst in plan],
            [
                "_staged/api/protos/experiment/api.proto",
                "_staged/api/protos/experiment/experiment.proto",
                "_staged/api/protos/xid/api.proto",
                "_staged/api/protos/xid/messages.proto",
                "_staged/api/protos/dashboard/dashboard_service.proto",
                "_staged/api/protos/dashboard/messages.proto",
                "_staged/xdash/__init__.py",
                "_staged/xdash/cli.py",
                "_staged/xdash/py.typed",
            ],
        )

    def test_sdist_plan_round_trips_through_staged_layout(self) -> None:
        layout = _make_sources(self.root)
        project_dir = self.root / "sdist"
        build_staging.copy_files(build_staging.sdist_copy_plan(layout), project_dir)

        staged = build_staging.resolve_source_layout(project_dir)

        self.assertEqual(
            [dst for _, dst in build_staging.proto_copy_plan(staged)],
            [dst for _, dst in build_staging.proto_copy_plan(layout)],
        )
        self.assertEqual(
            [dst for _, dst in build_staging.xdash_copy_plan(staged)],
            [dst for _, dst in build_staging.xdash_copy_plan(layout)],
        )

    def test_proto_plan_maps_to_existing_import_paths(self) -> None:
        layout = _make_sources(self.root)

        plan = build_staging.proto_copy_plan(layout)

        self.assertEqual(
            [str(dst) for _, dst in plan],
            [
                "xmanager_cloud/experiment_state_server/proto/api.proto",
                "xmanager_cloud/experiment_state_server/proto/experiment.proto",
                "xmanager_cloud/xid_service/proto/api.proto",
                "xmanager_cloud/xid_service/proto/messages.proto",
                "xmanager_cloud/dashboard_service/proto/dashboard_service.proto",
                "xmanager_cloud/dashboard_service/proto/messages.proto",
            ],
        )

    def test_xdash_plan_excludes_tests(self) -> None:
        layout = _make_sources(self.root)

        plan = build_staging.xdash_copy_plan(layout)

        self.assertEqual(
            [str(dst) for _, dst in plan],
            ["xdash/__init__.py", "xdash/cli.py", "xdash/py.typed"],
        )

    def test_empty_proto_directory_raises(self) -> None:
        layout = _make_sources(self.root)
        for proto in (layout.proto_root / "xid").glob("*.proto"):
            proto.unlink()

        with self.assertRaisesRegex(FileNotFoundError, "No .proto files"):
            build_staging.proto_copy_plan(layout)

    def test_missing_xdash_modules_raise(self) -> None:
        layout = build_staging.SourceLayout(proto_root=self.root, xdash_dir=self.root / "none")

        with self.assertRaisesRegex(FileNotFoundError, "No xdash modules"):
            build_staging.xdash_copy_plan(layout)

    def test_missing_xdash_py_typed_raises(self) -> None:
        layout = _make_sources(self.root)
        (layout.xdash_dir / "py.typed").unlink()

        with self.assertRaisesRegex(FileNotFoundError, "py.typed"):
            build_staging.xdash_copy_plan(layout)

    def test_nested_xdash_module_raises(self) -> None:
        layout = _make_sources(self.root)
        _write(layout.xdash_dir / "sub" / "module.py")

        with self.assertRaisesRegex(RuntimeError, "subpackages are not staged"):
            build_staging.xdash_copy_plan(layout)


class GeneratedNamesTest(BuildStagingTestCase):
    def test_generated_init_files_cover_every_proto_package(self) -> None:
        self.assertEqual(
            [str(path) for path in build_staging.generated_init_files()],
            [
                "xmanager_cloud/experiment_state_server/__init__.py",
                "xmanager_cloud/experiment_state_server/proto/__init__.py",
                "xmanager_cloud/xid_service/__init__.py",
                "xmanager_cloud/xid_service/proto/__init__.py",
                "xmanager_cloud/dashboard_service/__init__.py",
                "xmanager_cloud/dashboard_service/proto/__init__.py",
            ],
        )

    def test_expected_proto_modules(self) -> None:
        layout = _make_sources(self.root)

        modules = build_staging.expected_proto_modules(layout)

        self.assertEqual(len(modules), 12)
        self.assertIn("xmanager_cloud.experiment_state_server.proto.api_pb2", modules)
        self.assertIn("xmanager_cloud.xid_service.proto.messages_pb2_grpc", modules)
        self.assertIn("xmanager_cloud.dashboard_service.proto.dashboard_service_pb2", modules)

    def test_is_test_module(self) -> None:
        self.assertTrue(build_staging.is_test_module("cli_test"))
        self.assertFalse(build_staging.is_test_module("cli"))
        self.assertFalse(build_staging.is_test_module("test_data"))


class CopyFilesTest(BuildStagingTestCase):
    def test_copies_into_nested_destinations(self) -> None:
        source = self.root / "src.proto"
        _write(source, "syntax")

        build_staging.copy_files([(source, PurePosixPath("a/b/dst.proto"))], self.root / "out")

        self.assertEqual((self.root / "out" / "a" / "b" / "dst.proto").read_text(), "syntax")

    def test_write_init_files_creates_every_generated_package(self) -> None:
        build_staging.write_init_files(self.root)

        for init_file in build_staging.generated_init_files():
            self.assertTrue((self.root / init_file).is_file(), init_file)


class CompileProtosTest(BuildStagingTestCase):
    def test_passes_include_paths_outputs_and_sources_to_protoc(self) -> None:
        calls: list[list[str]] = []
        proto = PurePosixPath("xmanager_cloud/xid_service/proto/api.proto")

        def fake_protoc(args: list[str]) -> int:
            calls.append(args)
            return 0

        build_staging.compile_protos(
            proto_tree=self.root / "tree",
            proto_files=[proto],
            include_dirs=[self.root / "wkt", self.root / "googleapis"],
            output_dir=self.root / "out",
            protoc_main=fake_protoc,
        )

        self.assertEqual(
            calls,
            [
                [
                    "grpc_tools.protoc",
                    f"-I{self.root / 'tree'}",
                    f"-I{self.root / 'wkt'}",
                    f"-I{self.root / 'googleapis'}",
                    f"--python_out={self.root / 'out'}",
                    f"--grpc_python_out={self.root / 'out'}",
                    str(self.root / "tree" / proto),
                ]
            ],
        )

    def test_nonzero_protoc_status_raises(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "exit status 1"):
            build_staging.compile_protos(
                proto_tree=self.root,
                proto_files=[],
                include_dirs=[],
                output_dir=self.root,
                protoc_main=lambda _: 1,
            )


if __name__ == "__main__":
    unittest.main()
