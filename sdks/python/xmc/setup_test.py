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

"""Unit tests for the xmc-metrics build hooks in setup.py."""

import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock

from setuptools.command.build_py import build_py
from setuptools.command.sdist import sdist
from setuptools.dist import Distribution

from xmc import setup as xmc_setup

_PROTO_TEXT = 'syntax = "proto3";\n'


def _write_proto(root: Path) -> Path:
    """Writes a placeholder METRICS_PROTO under root and returns its path."""
    proto = root / xmc_setup.METRICS_PROTO
    proto.parent.mkdir(parents=True)
    proto.write_text(_PROTO_TEXT)
    return proto


def _fake_protoc(exit_code: int) -> tuple[types.ModuleType, mock.Mock]:
    """Returns a fake grpc_tools package whose protoc writes both outputs."""

    def main(args: list[str]) -> int:
        out_dir = Path(next(a for a in args if a.startswith("--python_out=")).split("=", 1)[1])
        generated = out_dir / xmc_setup.METRICS_PROTO.parent
        generated.mkdir(parents=True)
        (generated / "metrics_pb2.py").write_text("# py\n")
        (generated / "metrics_pb2.pyi").write_text("# pyi\n")
        return exit_code

    protoc = mock.Mock(side_effect=main)
    package = types.ModuleType("grpc_tools")
    package.protoc = types.SimpleNamespace(main=protoc)  # type: ignore[attr-defined]
    return package, protoc


class FindProtoRootTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = Path(tmp.name)
        self.project = self.repo / "sdks" / "python" / "xmc"
        self.project.mkdir(parents=True)

    def test_sdist_layout_uses_project_root(self):
        _write_proto(self.project)
        _write_proto(self.repo)

        self.assertEqual(xmc_setup.find_proto_root(self.project), self.project)

    def test_checkout_layout_uses_repository_root(self):
        _write_proto(self.repo)

        self.assertEqual(xmc_setup.find_proto_root(self.project), self.repo)

    def test_missing_proto_raises(self):
        with self.assertRaisesRegex(FileNotFoundError, "metrics.proto not found"):
            xmc_setup.find_proto_root(self.project)

    def test_shallow_project_root_without_proto_raises(self):
        with self.assertRaises(FileNotFoundError):
            xmc_setup.find_proto_root(Path("/"))


class CompileMetricsProtoTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.proto = _write_proto(self.root / "repo")
        self.out_dir = self.root / "out" / "xmc" / "_protos"

    def test_compiles_with_repository_include_root(self):
        package, protoc = _fake_protoc(exit_code=0)

        with mock.patch.dict(sys.modules, {"grpc_tools": package}):
            xmc_setup.compile_metrics_proto(self.root / "repo", self.out_dir)

        args = protoc.call_args.args[0]
        self.assertIn(f"-I{self.root / 'repo'}", args)
        self.assertEqual(args[-1], str(self.proto))
        self.assertEqual((self.out_dir / "metrics_pb2.py").read_text(), "# py\n")
        self.assertEqual((self.out_dir / "metrics_pb2.pyi").read_text(), "# pyi\n")

    def test_protoc_failure_raises(self):
        package, _ = _fake_protoc(exit_code=1)

        with (
            mock.patch.dict(sys.modules, {"grpc_tools": package}),
            self.assertRaisesRegex(RuntimeError, "protoc failed"),
        ):
            xmc_setup.compile_metrics_proto(self.root / "repo", self.out_dir)

        self.assertFalse(self.out_dir.exists())


class BuildPyWithMetricsProtoTest(unittest.TestCase):
    def _command(self, editable: bool) -> xmc_setup.BuildPyWithMetricsProto:
        command = xmc_setup.BuildPyWithMetricsProto(Distribution())
        command.build_lib = "/tmp/build/lib"
        command.editable_mode = editable
        return command

    def test_regular_build_writes_into_build_lib(self):
        self.assertEqual(
            self._command(editable=False).protos_out_dir(), Path("/tmp/build/lib/xmc/_protos")
        )

    def test_editable_build_writes_in_place(self):
        self.assertEqual(
            self._command(editable=True).protos_out_dir(),
            Path(xmc_setup.__file__).resolve().parent / "_protos",
        )

    def test_run_compiles_after_copying_sources(self):
        command = self._command(editable=False)
        calls: list[str] = []

        with (
            mock.patch.object(build_py, "run", lambda _: calls.append("copy")),
            mock.patch.object(xmc_setup, "find_proto_root", return_value=Path("/repo")),
            mock.patch.object(
                xmc_setup,
                "compile_metrics_proto",
                side_effect=lambda root, out: calls.append(f"compile {root} {out}"),
            ),
        ):
            command.run()

        self.assertEqual(calls, ["copy", "compile /repo /tmp/build/lib/xmc/_protos"])

    def test_find_package_modules_drops_build_script_and_tests(self):
        found = [
            ("xmc", "client", "./client.py"),
            ("xmc", "client_test", "./client_test.py"),
            ("xmc", "setup", "./setup.py"),
            ("xmc", "setup_test", "./setup_test.py"),
        ]

        with mock.patch.object(build_py, "find_package_modules", return_value=found):
            modules = self._command(editable=False).find_package_modules("xmc", ".")

        self.assertEqual(modules, [("xmc", "client", "./client.py")])


class SdistWithMetricsProtoTest(unittest.TestCase):
    def test_release_tree_contains_staged_proto(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo, release = Path(tmp, "repo"), Path(tmp, "release")
            _write_proto(repo)
            command = xmc_setup.SdistWithMetricsProto(Distribution())

            with (
                mock.patch.object(sdist, "make_release_tree") as base_tree,
                mock.patch.object(xmc_setup, "find_proto_root", return_value=repo),
            ):
                command.make_release_tree(str(release), ["setup.py"])

            base_tree.assert_called_once_with(str(release), ["setup.py"])
            staged = release / xmc_setup.METRICS_PROTO
            self.assertEqual(staged.read_text(), _PROTO_TEXT)


if __name__ == "__main__":
    unittest.main()
