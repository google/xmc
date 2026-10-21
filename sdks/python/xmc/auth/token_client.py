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

"""HTTP client for the XMC `/api/cli/token` endpoint."""

from __future__ import annotations

import dataclasses
import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

from xmc.auth import ACCESS_DENIED_MESSAGE, AuthError
from xmc.auth.deployment_server import is_loopback_host

TOKEN_PATH, _TIMEOUT_SECONDS = "/api/cli/token", 30
_MALFORMED = "The XMC server returned a malformed token response."


@dataclasses.dataclass(frozen=True)
class TokenGrant:
    """A CLI session token issued by the deployment. Never log `access_token`."""

    access_token: str
    email: str
    expires_in: int


class _RefuseRedirects(urllib.request.HTTPRedirectHandler):
    """Fails instead of following redirects, which could forward secrets cross-origin."""

    def redirect_request(self, req: Any, fp: Any, code: int, *_: Any) -> None:
        """Raises for every redirect."""
        msg = f"The XMC server answered with a redirect (HTTP {code}); refusing to follow it."
        raise AuthError(f"{msg} Check that --server points at the deployment itself.")


def build_opener(server_origin: str) -> urllib.request.OpenerDirector:
    """Returns an opener that refuses redirects and skips proxies for loopback."""
    handlers: list[urllib.request.BaseHandler] = [_RefuseRedirects()]
    if is_loopback_host(urllib.parse.urlsplit(server_origin).hostname or ""):
        handlers.append(urllib.request.ProxyHandler({}))
    return urllib.request.build_opener(*handlers)


def exchange_code(
    origin: str, code: str, verifier: str, opener: urllib.request.OpenerDirector | None = None
) -> TokenGrant:
    """Redeems a one-time login code and its PKCE verifier for a CLI token."""
    form = {"grant_type": "authorization_code", "code": code, "code_verifier": verifier}
    headers = {"Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded"}
    req = urllib.request.Request(
        origin + TOKEN_PATH, urllib.parse.urlencode(form).encode(), headers, method="POST"
    )
    try:
        with (opener or build_opener(origin)).open(req, timeout=_TIMEOUT_SECONDS) as resp:
            return parse_token_response(bytes(resp.read()))
    except urllib.error.HTTPError as e:
        raise AuthError(_token_error_message(e)) from None
    except (urllib.error.URLError, OSError) as e:
        raise AuthError(f"Could not reach the XMC server: {getattr(e, 'reason', e)}") from None


def _token_error_message(response: urllib.error.HTTPError) -> str:
    """Maps a token endpoint error response to an actionable message."""
    try:
        error = json.loads(response.read(4096)).get("error")
    except (OSError, ValueError, AttributeError):
        error = None
    if response.code == 403 or error == "access_denied":
        return ACCESS_DENIED_MESSAGE
    if response.code == 429 or error == "too_many_requests":
        return "Too many login attempts. Wait a minute, then run `xmc login` again."
    if error == "invalid_grant":
        return "The login code is invalid, expired, or already used. Run `xmc login` again."
    return f"The XMC server rejected the token request (HTTP {response.code})."


def parse_token_response(payload: bytes) -> TokenGrant:
    """Validates a token endpoint JSON response."""
    try:
        data = json.loads(payload)
    except ValueError:
        raise AuthError(_MALFORMED) from None
    if not isinstance(data, dict):
        raise AuthError(_MALFORMED)
    token, email, ttl = data.get("access_token"), data.get("email"), data.get("expires_in")
    bearer = str(data.get("token_type", "")).lower() == "bearer"
    if not (isinstance(token, str) and isinstance(email, str) and type(ttl) is int):
        raise AuthError(_MALFORMED)
    if not (token and email and bearer and ttl > 0):
        raise AuthError(_MALFORMED)
    return TokenGrant(access_token=token, email=email, expires_in=ttl)
