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
"""Authentication token resolution and XManager CLI credential auto-discovery."""

from __future__ import annotations

import json
import logging
import math
import os
import pathlib
import stat
import urllib.parse
from datetime import datetime, timezone
from typing import Any

logger = logging.getLogger(__name__)

CLI_TOKEN_EXPIRY_BUFFER_SEC = 30.0
LOCAL_HOSTS = frozenset({"localhost", "127.0.0.1", "::1", "0.0.0.0"})
LOOPBACK_HOSTS = frozenset({"localhost", "127.0.0.1", "::1"})
_GRPC_SCHEME_PREFIXES = ("dns:///", "dns://", "grpc://")


def resolve_env_auth_token() -> str | None:
    """Resolves an explicit bearer token from XMC_AUTH_TOKEN or AUTH_TOKEN.

    Returns:
        The stripped bearer token if configured, or None if unset/empty.

    Raises:
        ValueError: If XMC_AUTH_TOKEN or AUTH_TOKEN is set to a whitespace-only string.
    """
    for env_var in ("XMC_AUTH_TOKEN", "AUTH_TOKEN"):
        raw = os.environ.get(env_var)
        if not raw:
            continue
        if stripped := raw.strip():
            return stripped
        raise ValueError("XMC_AUTH_TOKEN must not be whitespace-only.")
    return None


def get_credentials_file_path() -> pathlib.Path:
    """Resolves the path to the XManager CLI credentials file."""
    if custom := os.path.expanduser(os.environ.get("XMC_CREDENTIALS_FILE", "").strip()):
        if os.path.isabs(custom):
            return pathlib.Path(custom)
    if xdg := os.path.expanduser(os.environ.get("XDG_CONFIG_HOME", "").strip()):
        if os.path.isabs(xdg):
            return pathlib.Path(xdg) / "xmc" / "credentials.json"
    return pathlib.Path.home() / ".config" / "xmc" / "credentials.json"


def _strip_grpc_host_prefix(host: str) -> str:
    if host.startswith("grpc.api."):
        return host[len("grpc.api.") :]
    return host[len("grpc.") :] if host.startswith("grpc.") else host


def _parse_url_origin(raw_url: str, *, allow_path: bool = True) -> str | None:
    parts = urllib.parse.urlsplit(raw_url.strip())
    if parts.username or parts.password or parts.query or parts.fragment:
        return None
    if not allow_path and parts.path not in ("", "/"):
        return None
    try:
        port = parts.port
    except ValueError:
        return None
    scheme, host = parts.scheme.lower(), _strip_grpc_host_prefix((parts.hostname or "").lower())
    if not host or scheme not in {"http", "https"}:
        return None
    if (scheme == "http" and host not in LOOPBACK_HOSTS) or (
        scheme == "https" and host in LOCAL_HOSTS and host not in LOOPBACK_HOSTS
    ):
        return None
    host_str = f"[{host}]" if ":" in host else host
    is_default = (scheme == "https" and port == 443) or (scheme == "http" and port == 80)
    return f"{scheme}://{host_str}:{port}" if port and not is_default else f"{scheme}://{host_str}"


def _extract_grpc_hostname(target: str) -> str:
    cleaned = target.strip()
    for prefix in _GRPC_SCHEME_PREFIXES:
        if cleaned.lower().startswith(prefix):
            cleaned = cleaned[len(prefix) :]
            break
    if "/" in cleaned:
        cleaned = cleaned.rsplit("/", 1)[-1]
    if cleaned.startswith("["):
        end = cleaned.find("]")
        return cleaned[1:end].lower() if end != -1 else ""
    return cleaned.rsplit(":", 1)[0].lower() if cleaned.count(":") == 1 else cleaned.lower()


def _resolve_env_origin() -> str | None:
    if xm_ep := os.environ.get("XMANAGER_ENDPOINT", "").strip():
        if xm_ep.lower().startswith(("http://", "https://")):
            if origin := _parse_url_origin(xm_ep):
                return origin
        elif (host := _extract_grpc_hostname(xm_ep)).startswith(("grpc.api.", "grpc.")):
            domain = _strip_grpc_host_prefix(host)
            if domain and domain not in LOCAL_HOSTS:
                return f"https://{domain}"
    for env_var in ("XMC_API_URL", "XMANAGER_UI_URL"):
        if (raw := os.environ.get(env_var, "").strip()) and (origin := _parse_url_origin(raw)):
            return origin
    if raw_domain := os.environ.get("XMC_DOMAIN", "").strip():
        candidate = raw_domain if "://" in raw_domain else f"https://{raw_domain}"
        return _parse_url_origin(candidate, allow_path=False)
    return None


def _matches_configured_origin(hostname: str, configured_origin: str) -> bool:
    origin_host = (urllib.parse.urlsplit(configured_origin).hostname or "").lower()
    return bool(origin_host) and hostname in {
        origin_host,
        f"grpc.{origin_host}",
        f"grpc.api.{origin_host}",
    }


def resolve_xmc_origin(endpoint: str | None = None, *, secure: bool = True) -> str | None:
    """Resolves the canonical XMC origin URL for credential lookup.

    Args:
        endpoint: Optional HTTP(S) URL or gRPC target address.
        secure: Whether the gRPC transport uses TLS (ignored for HTTP(S) URLs).

    Returns:
        The normalized origin (e.g. 'https://xmc.example.com') or None if unverifiable.
    """
    cleaned = (endpoint or "").strip()
    if cleaned.lower().startswith(("http://", "https://")):
        parsed = _parse_url_origin(cleaned.split("?", 1)[0].split("#", 1)[0])
        if not parsed:
            return None
        host = (urllib.parse.urlsplit(parsed).hostname or "").lower()
        return (_resolve_env_origin() or parsed) if host in LOOPBACK_HOSTS else parsed

    hostname = _extract_grpc_hostname(cleaned) if cleaned else ""
    if not secure and hostname and hostname not in LOOPBACK_HOSTS:
        return None
    if hostname.startswith(("grpc.api.", "grpc.")):
        domain = _strip_grpc_host_prefix(hostname)
        return f"https://{domain}" if domain and domain not in LOCAL_HOSTS else None

    configured = _resolve_env_origin()
    if not hostname or hostname in LOOPBACK_HOSTS:
        return configured
    return configured if configured and _matches_configured_origin(hostname, configured) else None


def _verify_credentials_file_stat(st: os.stat_result, creds_path: pathlib.Path) -> bool:
    if not stat.S_ISREG(st.st_mode):
        logger.warning("Ignoring XMC credentials file %s: not a regular file.", creds_path)
        return False
    if hasattr(os, "getuid") and st.st_uid != os.getuid():
        logger.warning(
            "Ignoring XMC credentials file %s: owned by uid %s, expected %s.",
            creds_path,
            st.st_uid,
            os.getuid(),
        )
        return False
    if (st.st_mode & 0o077) != 0:
        logger.warning(
            "Ignoring XMC credentials file %s: permissions %s are too open (expected 0600).",
            creds_path,
            oct(st.st_mode & 0o777),
        )
        return False
    return True


def _extract_valid_deployment_token(data: Any, origin: str, creds_path: pathlib.Path) -> str | None:
    if not isinstance(data, dict) or data.get("version") != 1:
        logger.warning("Ignoring XMC credentials file %s: unsupported schema version.", creds_path)
        return None
    deployments = data.get("deployments")
    entry = deployments.get(origin) if isinstance(deployments, dict) else None
    if not isinstance(entry, dict):
        return None
    token, expires_at = entry.get("access_token"), entry.get("expires_at")
    if not isinstance(token, str) or not token.strip() or isinstance(expires_at, bool):
        return None
    if not isinstance(expires_at, (int, float)) or not math.isfinite(expires_at):
        return None
    if expires_at <= datetime.now(timezone.utc).timestamp() + CLI_TOKEN_EXPIRY_BUFFER_SEC:
        logger.warning("Cached XMC CLI token for %s is expired; run 'xmanager login'.", origin)
        return None
    return token.strip()


def load_cached_cli_token(endpoint: str | None = None, *, secure: bool = True) -> str | None:
    """Loads a valid cached XManager CLI token for the resolved deployment origin.

    Args:
        endpoint: Optional HTTP(S) URL or gRPC target address.
        secure: Whether the gRPC transport uses TLS.

    Returns:
        The bearer token string if a valid, non-expired entry exists, else None.
    """
    origin = resolve_xmc_origin(endpoint, secure=secure)
    if not origin:
        return None
    creds_path = get_credentials_file_path()
    try:
        fd = os.open(creds_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    except FileNotFoundError:
        return None
    except OSError as err:
        logger.warning("Failed to open XMC credentials file %s: %s", creds_path, err)
        return None

    fd_owned = False
    try:
        if not _verify_credentials_file_stat(os.fstat(fd), creds_path):
            return None
        with os.fdopen(fd, "rb") as cred_file:
            fd_owned = True
            data = json.load(cred_file)
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as err:
        logger.warning("Failed to read XMC credentials file %s: %s", creds_path, err)
        return None
    finally:
        if not fd_owned:
            os.close(fd)
    return _extract_valid_deployment_token(data, origin, creds_path)


def mint_iam_impersonated_token(
    credentials: Any, google_requests: Any, client_sa: str, audience: str
) -> str:
    """Mints an OIDC ID token by impersonating client_sa via IAM Credentials API.

    Args:
        credentials: Google Auth credentials used to authorize the IAM request.
        google_requests: The google.auth.transport.requests module.
        client_sa: Service account email to impersonate.
        audience: Target OAuth client ID audience (IAP_CLIENT_ID).

    Returns:
        The signed OIDC ID token string.
    """
    session = google_requests.AuthorizedSession(credentials)
    try:
        url = (
            "https://iamcredentials.googleapis.com/v1/projects/-/"
            f"serviceAccounts/{client_sa}:generateIdToken"
        )
        response = session.post(url, json={"audience": audience, "includeEmail": True}, timeout=10)
        response.raise_for_status()
        return str(response.json()["token"]).strip()
    finally:
        session.close()


def _fetch_oidc_fallback_token() -> str | None:
    iap_id = (os.environ.get("IAP_CLIENT_ID") or os.environ.get("CLIENT_ID") or "").strip()
    if not iap_id:
        return None
    client_sa = (os.environ.get("XMC_CLIENT_SA") or "").strip()
    try:
        import google.auth
        from google.auth.transport import requests as google_requests

        if client_sa:
            creds, _ = google.auth.default()
            return mint_iam_impersonated_token(creds, google_requests, client_sa, iap_id) or None
        from google.oauth2 import id_token

        token = id_token.fetch_id_token(google_requests.Request(), iap_id)
        return str(token).strip() if token else None
    except Exception as err:
        logger.warning("Could not retrieve OIDC ID token: %s", err)
        return None


def resolve_auth_headers(
    endpoint: str | None = None,
    *,
    content_type: str | None = "application/json",
) -> dict[str, str]:
    """Builds HTTP request headers with Bearer authentication for XMC REST gateways.

    Args:
        endpoint: Optional HTTP(S) URL or gRPC target address for origin binding.
        content_type: Optional Content-Type header value (defaults to 'application/json').

    Returns:
        Dictionary of HTTP headers including 'Authorization' when a token is resolved.
    """
    headers = {"Content-Type": content_type} if content_type else {}
    token = (
        resolve_env_auth_token() or load_cached_cli_token(endpoint) or _fetch_oidc_fallback_token()
    )
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return headers
