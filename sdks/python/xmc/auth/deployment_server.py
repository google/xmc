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

from xmc.auth import AuthError

SERVER_ENV_VARS = ("XMC_API_URL", "XMANAGER_UI_URL")
DOMAIN_ENV_VAR = "XMC_DOMAIN"


def is_loopback_host(host: str) -> bool:
    """Reports whether `host` is a loopback IP literal or `localhost`."""
    try:
        return host.lower() == "localhost" or ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _server_from_domain(domain: str | None) -> str | None:
    """Converts `XMC_DOMAIN` (a bare hostname or a full URL) into a server URL."""
    cleaned = (domain or "").strip()
    if not cleaned:
        return None
    return cleaned if "://" in cleaned else f"https://{cleaned}"


def resolve_server(flag_value: str | None, env: Mapping[str, str]) -> str:
    """Returns the origin from `--server`, XMC_API_URL, XMANAGER_UI_URL, or XMC_DOMAIN."""
    candidates = (
        flag_value,
        *(env.get(name) for name in SERVER_ENV_VARS),
        _server_from_domain(env.get(DOMAIN_ENV_VAR)),
    )
    raw = next((value.strip() for value in candidates if value and value.strip()), None)
    if raw is None:
        raise AuthError(
            "No XMC server configured. "
            "Pass --server or set XMC_API_URL, XMANAGER_UI_URL, or XMC_DOMAIN."
        )
    return parse_server(raw)


def parse_server(raw: str) -> str:
    """Normalizes a URL to its origin (`scheme://host[:port]`); https unless loopback."""
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
