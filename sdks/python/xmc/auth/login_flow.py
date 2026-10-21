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

"""Orchestrates the browser (loopback) CLI login."""

from __future__ import annotations

import base64
import dataclasses
import hashlib
import secrets
import threading
import urllib.parse
from collections.abc import Callable
from typing import TextIO

from xmc.auth.loopback import CallbackReceiver
from xmc.auth.token_client import TokenGrant

LOGIN_PATH, SUCCESS_PATH, DEFAULT_TIMEOUT_SECONDS = "/api/cli/login", "/api/cli/success", 300


def s256_challenge(verifier: str) -> str:
    """Returns BASE64URL(SHA256(verifier)) without padding (RFC 7636 section 4.2)."""
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def new_verifier() -> str:
    """Returns a random 86-character code verifier (RFC 7636 allows 43-128)."""
    return secrets.token_urlsafe(64)


def new_state() -> str:
    """Returns an unguessable OAuth `state` value carrying 256 random bits."""
    return secrets.token_urlsafe(32)


@dataclasses.dataclass(frozen=True)
class LoginDependencies:
    """Side-effecting collaborators of the login flow, injectable for tests."""

    out: TextIO
    open_browser: Callable[[str], bool]
    exchange: Callable[[str, str, str], TokenGrant]


def build_login_url(
    server_origin: str, state: str, challenge: str, port: int, login_hint: str | None = None
) -> str:
    """Builds the `GET /api/cli/login` URL for a loopback listener on `port`."""
    hint = {"login_hint": login_hint} if login_hint else {}
    params = {"port": str(port), "state": state, "code_challenge": challenge}
    params |= {"code_challenge_method": "S256", **hint}
    return f"{server_origin}{LOGIN_PATH}?{urllib.parse.urlencode(params)}"


def run_login(
    origin: str, deps: LoginDependencies, timeout: float = 300, login_hint: str | None = None
) -> TokenGrant:
    """Runs one browser login and returns the issued token."""
    verifier, state = new_verifier(), new_state()
    with CallbackReceiver(state, origin + SUCCESS_PATH) as receiver:
        url = build_login_url(origin, state, s256_challenge(verifier), receiver.port, login_hint)
        prompt = f"Opening your browser to sign in to {origin}.\nIf it does not open, visit:"
        deps.out.write(f"{prompt}\n\n  {url}\n\n")
        threading.Thread(target=deps.open_browser, args=(url,), daemon=True).start()
        code = receiver.wait_for_code(timeout)
    return deps.exchange(origin, code, verifier)
