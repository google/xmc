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

"""Rejects legacy `xmanager` wheels that bundle their own `xmanager_cloud` protos.

`xmanager < 0.9.0rc1` ships `xmanager_cloud/*_pb2.py` and `xdash/` itself. Loading
those next to this package registers the `cloud.xmanager.v1` descriptors twice
and crashes the process with a `TypeError` from the protobuf descriptor pool, so
this check fails early with an actionable message instead.
"""

from __future__ import annotations

import warnings
from importlib import metadata

from packaging.version import InvalidVersion, Version

# First xmanager release that no longer bundles xmanager_cloud or xdash. The RC
# floor (not 0.9.0) keeps the 0.9.0rc1 rehearsal release installable.
MIN_XMANAGER_VERSION = Version("0.9.0rc1")


def _parse_version(installed: str | None) -> Version | None:
    """Parses an installed version string; None if it is missing or invalid."""
    if installed is None:
        return None
    try:
        return Version(installed)
    except InvalidVersion:
        return None


def check_xmanager_compatibility() -> None:
    """Raises if an installed `xmanager` still bundles `xmanager_cloud`.

    An absent `xmanager` is allowed. An unparsable version only warns, because it
    cannot be proven to be a legacy release.

    Raises:
        ImportError: If `xmanager` older than 0.9.0rc1 is installed.
    """
    try:
        installed: str | None = metadata.version("xmanager")
    except metadata.PackageNotFoundError:
        return
    except KeyError:
        # A dist-info without a Version field: Python 3.14+ raises KeyError, while
        # older versions return None.
        installed = None
    version = _parse_version(installed)
    if version is None:
        warnings.warn(
            f"Cannot parse the installed xmanager version {installed!r}; skipping the "
            f"xmanager>={MIN_XMANAGER_VERSION} compatibility check.",
            RuntimeWarning,
            stacklevel=2,
        )
        return
    if version < MIN_XMANAGER_VERSION:
        raise ImportError(
            f"xmanager-cloud is incompatible with xmanager {installed}, which bundles its "
            "own xmanager_cloud protos and xdash. Upgrading xmanager deletes those shared "
            "files, so reinstall xmanager-cloud afterwards: "
            'pip install -U "xmanager>=0.9.0" && '
            "pip install --force-reinstall --no-deps xmanager-cloud "
            "(add --pre to the first command while only a 0.9.0 release candidate exists)."
        )
