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

"""Unit tests for the legacy xmanager compatibility guard.

Bazel has no PyPI dependencies (`packaging`), so this suite runs against the
installed package in `.github/workflows/xmanager-cloud-package.yml`.
"""

import unittest
import warnings
from importlib import metadata
from unittest import mock

from xmanager_cloud import _version_guard


class CheckXmanagerCompatibilityTest(unittest.TestCase):
    """Covers every branch of check_xmanager_compatibility."""

    def _patch_installed_version(
        self, return_value: str | None = None, side_effect: type[Exception] | None = None
    ) -> mock.MagicMock:
        patcher = mock.patch.object(
            _version_guard.metadata, "version", return_value=return_value, side_effect=side_effect
        )
        self.addCleanup(patcher.stop)
        return patcher.start()

    def test_absent_xmanager_is_allowed(self) -> None:
        version = self._patch_installed_version(side_effect=metadata.PackageNotFoundError)

        _version_guard.check_xmanager_compatibility()

        version.assert_called_once_with("xmanager")

    def test_legacy_xmanager_raises_import_error(self) -> None:
        self._patch_installed_version(return_value="0.8.5.post1")

        with self.assertRaisesRegex(
            ImportError,
            r'pip install -U "xmanager>=0\.9\.0" && '
            r"pip install --force-reinstall --no-deps xmanager-cloud",
        ):
            _version_guard.check_xmanager_compatibility()

    def test_dev_build_below_rc_floor_raises_import_error(self) -> None:
        self._patch_installed_version(return_value="0.9.0.dev1")

        with self.assertRaisesRegex(ImportError, r"xmanager 0\.9\.0\.dev1"):
            _version_guard.check_xmanager_compatibility()

    def test_supported_xmanager_versions_are_allowed(self) -> None:
        for installed in ("0.9.0rc1", "0.9.0", "0.10.2"):
            with self.subTest(installed=installed):
                self._patch_installed_version(return_value=installed)
                with warnings.catch_warnings():
                    warnings.simplefilter("error")
                    _version_guard.check_xmanager_compatibility()

    def test_unparsable_version_warns_and_is_allowed(self) -> None:
        self._patch_installed_version(return_value="not-a-version")

        with self.assertWarnsRegex(RuntimeWarning, r"'not-a-version'"):
            _version_guard.check_xmanager_compatibility()

    def test_missing_version_metadata_warns_and_is_allowed(self) -> None:
        # Python 3.11-3.13 return None for a dist-info without a Version field.
        self._patch_installed_version(return_value=None)

        with self.assertWarnsRegex(RuntimeWarning, r"version None"):
            _version_guard.check_xmanager_compatibility()

    def test_missing_version_key_warns_and_is_allowed(self) -> None:
        # Python 3.14+ raises KeyError for a dist-info without a Version field.
        self._patch_installed_version(side_effect=KeyError)

        with self.assertWarnsRegex(RuntimeWarning, r"version None"):
            _version_guard.check_xmanager_compatibility()


if __name__ == "__main__":
    unittest.main()
