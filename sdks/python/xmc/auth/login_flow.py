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

import dataclasses
import threading
import urllib.parse
from collections.abc import Callable
from typing import TextIO

from xmc.auth import pkce
from xmc.auth.loopback import CallbackReceiver
from xmc.auth.token_client import TokenGrant

LOGIN_PATH = "/api/cli/login"
SUCCESS_PATH = "/api/cli/success"
DEFAULT_TIMEOUT_SECONDS = 300


@dataclasses.dataclass(frozen=True)
class LoginDependencies:
    """Side-effecting collaborators of the login flow, injectable for tests."""

    out: TextIO
    open_browser: Callable[[str], bool]  # webbrowser.open
    exchange: Callable[[str, str, str], TokenGrant]  # token_client.exchange_code


def build_login_url(
    server_origin: str, state: str, challenge: str, port: int, login_hint: str | None = None
) -> str:
    """Builds the `GET /api/cli/login` URL for a loopback listener on `port`."""
    params = {"port": str(port), "state": state, "code_challenge": challenge}
    params["code_challenge_method"] = "S256"
    if login_hint:
        params["login_hint"] = login_hint
    return f"{server_origin}{LOGIN_PATH}?{urllib.parse.urlencode(params)}"


def run_login(
    server_origin: str,
    deps: LoginDependencies,
    timeout: float = DEFAULT_TIMEOUT_SECONDS,
    login_hint: str | None = None,
) -> TokenGrant:
    """Runs one browser login and returns the issued token.

    Raises:
        AuthError: If the login fails, is denied, or times out.
    """
    verifier, state = pkce.new_verifier(), pkce.new_state()
    challenge = pkce.s256_challenge(verifier)
    with CallbackReceiver(state, server_origin + SUCCESS_PATH) as receiver:
        url = build_login_url(server_origin, state, challenge, receiver.port, login_hint)
        deps.out.write(
            f"Opening your browser to sign in to {server_origin}.\n"
            f"If it does not open, visit:\n\n  {url}\n\n"
        )
        # Off-thread: blocking browser commands (e.g. `BROWSER=w3m`) return only
        # after loading the loopback redirect, which wait_for_code serves.
        threading.Thread(target=deps.open_browser, args=(url,), daemon=True).start()
        code = receiver.wait_for_code(timeout)
    return deps.exchange(server_origin, code, verifier)
