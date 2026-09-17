# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Dex OpenID Connect token retriever for local development and E2E testing."""

from __future__ import annotations

import json
import logging
import urllib.error
import urllib.parse
import urllib.request

from test.e2e.lib.config import (
    DEFAULT_DEV_EMAIL,
    DEFAULT_DEV_PASSWORD,
    DEFAULT_DEX_CLIENT_ID,
    DEFAULT_DEX_CLIENT_SECRET,
    DEFAULT_DEX_URL,
)

logger = logging.getLogger(__name__)

DEFAULT_CLIENT_ID = DEFAULT_DEX_CLIENT_ID
DEFAULT_CLIENT_SECRET = DEFAULT_DEX_CLIENT_SECRET


def fetch_dex_oidc_token(
    dex_url: str = DEFAULT_DEX_URL,
    email: str = DEFAULT_DEV_EMAIL,
    password: str = DEFAULT_DEV_PASSWORD,
    client_id: str = DEFAULT_CLIENT_ID,
    client_secret: str = DEFAULT_CLIENT_SECRET,
    timeout_sec: float = 10.0,
) -> str:
    """Obtains a valid RS256 OIDC JWT from Dex using OAuth2 password grant."""
    token_endpoint = (
        f"{dex_url.rstrip('/')}/dex/token" if not dex_url.endswith("/dex") else f"{dex_url}/token"
    )
    if "/dex" not in token_endpoint:
        token_endpoint = f"{dex_url.rstrip('/')}/dex/token"

    params = {
        "grant_type": "password",
        "username": email,
        "password": password,
        "client_id": client_id,
        "client_secret": client_secret,
        "scope": "openid email profile federated:id",
    }
    data = urllib.parse.urlencode(params).encode("utf-8")
    req = urllib.request.Request(token_endpoint, data=data, method="POST")
    req.add_header("Content-Type", "application/x-www-form-urlencoded")

    try:
        with urllib.request.urlopen(req, timeout=timeout_sec) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            token = body.get("id_token") or body.get("access_token")
            if not token:
                raise RuntimeError(f"No id_token found in Dex response: {body}")
            return str(token)
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(
            f"Failed to fetch Dex OIDC token from {token_endpoint} (HTTP {e.code}): {err_body}"
        ) from e
    except Exception as e:
        raise RuntimeError(f"Failed to connect to Dex at {token_endpoint}: {e}") from e
