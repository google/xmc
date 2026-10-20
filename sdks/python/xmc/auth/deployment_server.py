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

"""Resolves the origin of the XMC deployment the CLI authenticates against."""

from __future__ import annotations

import ipaddress
import urllib.parse
from collections.abc import Mapping

from xmc.auth.errors import AuthError

SERVER_ENV_VARS = ("XMC_API_URL", "XMANAGER_UI_URL")


def is_loopback_host(host: str) -> bool:
    """Reports whether `host` is a loopback IP literal or `localhost`.

    `localhost` is accepted for local development only: it depends on name
    resolution, so prefer `127.0.0.1` or `[::1]`.
    """
    try:
        return host.lower() == "localhost" or ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def resolve_server(flag_value: str | None, env: Mapping[str, str]) -> str:
    """Returns the origin from `--server`, then XMC_API_URL, then XMANAGER_UI_URL.

    Raises:
        AuthError: If no deployment is configured or its URL is invalid.
    """
    candidates = [flag_value, *(env.get(name) for name in SERVER_ENV_VARS)]
    raw = next((value.strip() for value in candidates if value and value.strip()), None)
    if raw is None:
        raise AuthError(
            "No XMC server configured. Pass --server or set XMC_API_URL or XMANAGER_UI_URL."
        )
    return parse_server(raw)


def parse_server(raw: str) -> str:
    """Normalizes a URL to its origin (`scheme://host[:port]`); https unless loopback.

    Raises:
        AuthError: If the URL is malformed, carries credentials, or is insecure.
    """
    parts = urllib.parse.urlsplit(raw)
    try:
        host, port = (parts.hostname or "").lower(), parts.port
    except ValueError:
        host, port = "", None
    if parts.scheme not in ("http", "https") or not host or parts.username is not None:
        raise AuthError(f"Invalid XMC server URL: {raw!r}")
    if parts.scheme == "http" and not is_loopback_host(host):
        raise AuthError(f"XMC server URL must use https:// (got {raw!r}).")
    netloc = f"[{host}]" if ":" in host else host
    return f"{parts.scheme}://{netloc}" + ("" if port is None else f":{port}")
