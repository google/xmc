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

"""Unit tests for install_smoke_check."""

import json
import tempfile
import types
import unittest
from importlib import metadata
from pathlib import Path, PurePosixPath
from unittest import mock

import build_staging
import install_smoke_check


class InstallSmokeCheckTestCase(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name).resolve()
        self.site_packages = self.root / "site-packages"

    def _module(self, path: Path | None) -> types.ModuleType:
        module = types.ModuleType("fake")
        module.__file__ = None if path is None else str(path)
        return module


class ModulesToCheckTest(InstallSmokeCheckTestCase):
    def test_lists_sdk_modules_then_generated_protos(self) -> None:
        layout = build_staging.SourceLayout(proto_root=self.root, xdash_dir=self.root)
        with mock.patch.object(
            build_staging, "expected_proto_modules", return_value=["a.proto.x_pb2"]
        ):
            modules = install_smoke_check.modules_to_check(layout)

        self.assertEqual(modules[0], "xmanager_cloud")
        self.assertIn("xdash.auth", modules)
        self.assertEqual(
            [name for name in modules if name.startswith("xm_cloud.cli.")],
            [
                "xm_cloud.cli.arg_parsing",
                "xm_cloud.cli.credentials",
                "xm_cloud.cli.login_command",
                "xm_cloud.cli.logout_command",
                "xm_cloud.cli.main",
                "xm_cloud.cli.terminal_text",
            ],
        )
        self.assertEqual(modules[-1], "a.proto.x_pb2")


class ImportFailuresTest(InstallSmokeCheckTestCase):
    def _import_failures(self, modules: dict[str, object]) -> dict[str, str]:
        def fake_import(name: str) -> object:
            result = modules[name]
            if isinstance(result, Exception):
                raise result
            return result

        with mock.patch.object(install_smoke_check.importlib, "import_module", fake_import):
            return install_smoke_check.import_failures(list(modules), self.site_packages)

    def test_installed_modules_pass(self) -> None:
        failures = self._import_failures(
            {"xdash": self._module(self.site_packages / "xdash" / "__init__.py")}
        )

        self.assertEqual(failures, {})

    def test_reports_every_import_error(self) -> None:
        failures = self._import_failures(
            {
                "xmanager_cloud": ImportError("cannot import name 'x'"),
                "xdash": TypeError("duplicate symbol"),
            }
        )

        self.assertEqual(
            failures,
            {
                "xmanager_cloud": "ImportError: cannot import name 'x'",
                "xdash": "TypeError: duplicate symbol",
            },
        )

    def test_reports_modules_loaded_outside_site_packages(self) -> None:
        failures = self._import_failures({"xdash": self._module(self.root / "repo" / "xdash.py")})

        self.assertIn("outside", failures["xdash"])

    def test_reports_modules_without_file(self) -> None:
        failures = self._import_failures({"xdash": self._module(None)})

        self.assertEqual(failures, {"xdash": "has no __file__ (namespace package?)"})

    def test_reports_modules_missing_file_attribute(self) -> None:
        failures = self._import_failures({"xdash": types.ModuleType("fake")})

        self.assertEqual(failures, {"xdash": "has no __file__ (namespace package?)"})


class EnvironmentFailuresTest(InstallSmokeCheckTestCase):
    def _environment_failures(
        self, version: mock.MagicMock, scripts: list[mock.MagicMock]
    ) -> dict[str, str]:
        with (
            mock.patch.object(install_smoke_check.metadata, "version", version),
            mock.patch.object(
                install_smoke_check.metadata, "entry_points", return_value=scripts
            ) as entry_points,
        ):
            failures = install_smoke_check.environment_failures()
        version.assert_called_once_with("xmanager")
        self.assertEqual(
            entry_points.call_args_list,
            [
                mock.call(group="console_scripts", name="xdash"),
                mock.call(group="console_scripts", name="xmc"),
            ],
        )
        return failures

    def test_clean_environment_passes(self) -> None:
        version = mock.MagicMock(side_effect=metadata.PackageNotFoundError)

        self.assertEqual(self._environment_failures(version, [mock.MagicMock()]), {})

    def test_installed_xmanager_fails(self) -> None:
        version = mock.MagicMock(return_value="0.8.5.post1")

        failures = self._environment_failures(version, [mock.MagicMock()])

        self.assertEqual(failures, {"xmanager": "must not be installed, found 0.8.5.post1"})

    def test_missing_console_scripts_fail(self) -> None:
        version = mock.MagicMock(side_effect=metadata.PackageNotFoundError)

        failures = self._environment_failures(version, [])

        self.assertEqual(
            failures,
            {"xdash console script": "not registered", "xmc console script": "not registered"},
        )

    def test_console_script_load_error_fails(self) -> None:
        version = mock.MagicMock(side_effect=metadata.PackageNotFoundError)
        script = mock.MagicMock()
        script.load.side_effect = ModuleNotFoundError("No module named 'xm_cloud'")

        failures = self._environment_failures(version, [script])

        self.assertEqual(
            failures,
            {
                "xdash console script": "ModuleNotFoundError: No module named 'xm_cloud'",
                "xmc console script": "ModuleNotFoundError: No module named 'xm_cloud'",
            },
        )

    def test_reports_only_the_failing_console_script(self) -> None:
        broken = mock.MagicMock()
        broken.load.side_effect = ImportError("cannot import name 'main'")

        def entry_points(group: str, name: str) -> list[mock.MagicMock]:
            del group
            return [broken] if name == "xmc" else [mock.MagicMock()]

        with (
            mock.patch.object(
                install_smoke_check.metadata,
                "version",
                side_effect=metadata.PackageNotFoundError,
            ),
            mock.patch.object(install_smoke_check.metadata, "entry_points", entry_points),
        ):
            failures = install_smoke_check.environment_failures()

        self.assertEqual(failures, {"xmc console script": "ImportError: cannot import name 'main'"})


class PackagingFailuresTest(InstallSmokeCheckTestCase):
    def _packaging_failures(self, files: list[str] | None) -> dict[str, str]:
        paths = None if files is None else [PurePosixPath(name) for name in files]
        with mock.patch.object(install_smoke_check.metadata, "files", return_value=paths) as found:
            failures = install_smoke_check.packaging_failures()
        found.assert_called_once_with("xmanager-cloud")
        return failures

    def test_no_test_modules_passes(self) -> None:
        files = ["xmanager_cloud/_version_guard.py", "xdash/cli.py"]

        self.assertEqual(self._packaging_failures(files), {})

    def test_packaged_test_modules_fail(self) -> None:
        files = ["xmanager_cloud/_version_guard_test.py", "xdash/cli_test.py", "xdash/cli.py"]

        self.assertEqual(
            self._packaging_failures(files),
            {"packaged tests": "xdash/cli_test.py, xmanager_cloud/_version_guard_test.py"},
        )

    def test_missing_record_fails(self) -> None:
        self.assertEqual(
            self._packaging_failures(None),
            {"packaged tests": "xmanager-cloud has no RECORD file list"},
        )

    def test_distribution_not_installed_fails(self) -> None:
        with mock.patch.object(
            install_smoke_check.metadata, "files", side_effect=metadata.PackageNotFoundError
        ):
            failures = install_smoke_check.packaging_failures()

        self.assertEqual(failures, {"packaged tests": "xmanager-cloud is not installed"})


class MainTest(InstallSmokeCheckTestCase):
    def _main(
        self,
        import_failures: dict[str, str],
        env_failures: dict[str, str] | None = None,
        packaging_failures: dict[str, str] | None = None,
    ) -> tuple[int, dict[str, object]]:
        with (
            mock.patch.object(build_staging, "resolve_source_layout"),
            mock.patch.object(install_smoke_check, "modules_to_check", return_value=["m"]),
            mock.patch.object(install_smoke_check, "import_failures", return_value=import_failures),
            mock.patch.object(
                install_smoke_check, "environment_failures", return_value=env_failures or {}
            ),
            mock.patch.object(
                install_smoke_check, "packaging_failures", return_value=packaging_failures or {}
            ),
            mock.patch("builtins.print") as printed,
        ):
            status = install_smoke_check.main()
        return status, json.loads(printed.call_args.args[0])

    def test_success_exits_zero(self) -> None:
        status, output = self._main({})

        self.assertEqual(status, 0)
        self.assertEqual(output, {"status": "success", "checked": 1, "failures": {}})

    def test_import_failure_exits_one(self) -> None:
        status, output = self._main({"m": "TypeError: boom"})

        self.assertEqual(status, 1)
        self.assertEqual(
            output, {"status": "failure", "checked": 1, "failures": {"m": "TypeError: boom"}}
        )

    def test_environment_failure_exits_one(self) -> None:
        status, output = self._main({}, {"xmanager": "must not be installed, found 0.8.5"})

        self.assertEqual(status, 1)
        self.assertEqual(output["failures"], {"xmanager": "must not be installed, found 0.8.5"})

    def test_packaging_failure_exits_one(self) -> None:
        packaged = {"packaged tests": "xmanager_cloud/_version_guard_test.py"}
        status, output = self._main({}, packaging_failures=packaged)

        self.assertEqual(status, 1)
        self.assertEqual(output["failures"], packaged)

    def test_missing_sources_exit_two_with_json_error(self) -> None:
        with (
            mock.patch.object(
                build_staging, "resolve_source_layout", side_effect=FileNotFoundError("no protos")
            ),
            mock.patch("builtins.print") as printed,
        ):
            status = install_smoke_check.main()

        self.assertEqual(status, 2)
        self.assertIn('"error": "no protos"', printed.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
