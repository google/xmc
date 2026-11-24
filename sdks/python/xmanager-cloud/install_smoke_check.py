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

"""Import-checks an installed xmanager-cloud distribution.

Run it with the interpreter of a clean virtualenv that has the wheel or sdist
installed and no `xmanager`:

    <venv>/bin/python sdks/python/xmanager-cloud/install_smoke_check.py

It imports every generated proto module, the bundled xdash SDK, and the xdash
console script, requires each to load from the virtualenv rather than the
repository, and checks that no `*_test.py` module was packaged. Prints a JSON
summary and exits 0 on success, 1 when a check fails, and 2 when the
repository's proto or xdash sources cannot be found.
"""

from __future__ import annotations

import contextlib
import importlib
import json
import sys
import sysconfig
from collections.abc import Sequence
from importlib import metadata
from pathlib import Path

import build_staging

_PROJECT_DIR = Path(__file__).resolve().parent
_DISTRIBUTION = "xmanager-cloud"
# Importing xmanager_cloud runs the legacy xmanager guard.
_SDK_MODULES = ("xmanager_cloud", "xdash", "xdash.auth", "xdash.cli", "xdash.client")


def modules_to_check(layout: build_staging.SourceLayout) -> list[str]:
    """Lists the SDK modules plus every generated proto module."""
    return [*_SDK_MODULES, *build_staging.expected_proto_modules(layout)]


def import_failures(modules: Sequence[str], installed_root: Path) -> dict[str, str]:
    """Imports each module and reports the ones that fail or load from elsewhere.

    Args:
        modules: Fully qualified module names.
        installed_root: The virtualenv's site-packages directory.

    Returns:
        A mapping of module name to failure reason; empty when all pass.
    """
    failures: dict[str, str] = {}
    for name in modules:
        try:
            module = importlib.import_module(name)
        except Exception as err:
            failures[name] = f"{type(err).__name__}: {err}"
            continue
        module_file = getattr(module, "__file__", None)
        if module_file is None:
            failures[name] = "has no __file__ (namespace package?)"
            continue
        origin = Path(module_file).resolve()
        if not origin.is_relative_to(installed_root):
            failures[name] = f"loaded from {origin}, outside {installed_root}"
    return failures


def environment_failures() -> dict[str, str]:
    """Checks that xmanager is absent and the xdash console script loads."""
    failures: dict[str, str] = {}
    with contextlib.suppress(metadata.PackageNotFoundError):
        failures["xmanager"] = f"must not be installed, found {metadata.version('xmanager')}"
    scripts = metadata.entry_points(group="console_scripts", name="xdash")
    if not scripts:
        return {**failures, "xdash console script": "not registered"}
    try:
        next(iter(scripts)).load()
    except Exception as err:
        failures["xdash console script"] = f"{type(err).__name__}: {err}"
    return failures


def packaging_failures() -> dict[str, str]:
    """Checks that the installed distribution ships no unit test modules."""
    try:
        files = metadata.files(_DISTRIBUTION) or []
    except metadata.PackageNotFoundError:
        return {"packaged tests": f"{_DISTRIBUTION} is not installed"}
    shipped = sorted(
        str(path)
        for path in files
        if path.suffix == ".py" and build_staging.is_test_module(path.stem)
    )
    return {"packaged tests": ", ".join(shipped)} if shipped else {}


def main() -> int:
    """Runs the smoke check and prints a JSON summary."""
    try:
        modules = modules_to_check(build_staging.resolve_source_layout(_PROJECT_DIR))
    except FileNotFoundError as err:
        print(json.dumps({"status": "error", "error": str(err)}, indent=2))
        return 2
    installed_root = Path(sysconfig.get_paths()["purelib"]).resolve()
    failures = {
        **import_failures(modules, installed_root),
        **environment_failures(),
        **packaging_failures(),
    }
    summary = {"status": "failure" if failures else "success", "checked": len(modules)}
    print(json.dumps({**summary, "failures": failures}, indent=2))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
