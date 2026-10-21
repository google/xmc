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

"""Orchestrates browser (loopback) and manual (paste-a-code) CLI logins."""

from __future__ import annotations

import base64
import dataclasses
import hashlib
import secrets
import sys
import urllib.parse
from collections.abc import Callable, Mapping
from typing import TextIO

from xmc.auth import AuthError
from xmc.auth.loopback import CallbackReceiver
from xmc.auth.token_client import TokenGrant

LOGIN_PATH, SUCCESS_PATH, DEFAULT_TIMEOUT_SECONDS = "/api/cli/login", "/api/cli/success", 300
MANUAL_CODE_PROMPT = "Paste the code shown in your browser: "
MANUAL_WARNING = (
    "WARNING: Only paste a code from a login you started yourself, just now, in this "
    "terminal. Never paste a code someone else sent you, and never share yours."
)
_DESKTOP_PLATFORMS = ("darwin", "win32", "cygwin", "msys")


def browser_likely_available(env: Mapping[str, str], platform: str = sys.platform) -> bool:
    """Guesses whether a graphical browser can be opened on this machine.

    Returns:
        False for SSH sessions and for Unix sessions without X11 or Wayland.
    """
    if env.get("SSH_CONNECTION") or env.get("SSH_TTY"):
        return False
    if platform.startswith(_DESKTOP_PLATFORMS):
        return True
    return bool(env.get("DISPLAY") or env.get("WAYLAND_DISPLAY"))


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
    read_code: Callable[[str], str]
    exchange: Callable[[str, str, str], TokenGrant]


def build_login_url(
    server_origin: str,
    state: str,
    challenge: str,
    port: int | None = None,
    login_hint: str | None = None,
) -> str:
    """Builds the `GET /api/cli/login` URL; `port=None` selects manual mode."""
    hint = {"login_hint": login_hint} if login_hint else {}
    params = {"port": str(port)} if port is not None else {"mode": "manual"}
    params |= {"state": state, "code_challenge": challenge, "code_challenge_method": "S256", **hint}
    return f"{server_origin}{LOGIN_PATH}?{urllib.parse.urlencode(params)}"


def run_login(
    server_origin: str,
    deps: LoginDependencies,
    use_browser: bool,
    timeout: float = DEFAULT_TIMEOUT_SECONDS,
    login_hint: str | None = None,
) -> TokenGrant:
    """Runs one login, trying the browser first if `use_browser`, else manual mode.

    A manual fallback starts a fresh attempt (new state and PKCE verifier).

    Raises:
        AuthError: If the login fails, is denied, or times out.
    """
    attempt = _browser_attempt(server_origin, deps, timeout, login_hint) if use_browser else None
    code, verifier = attempt or _manual_attempt(server_origin, deps, login_hint)
    return deps.exchange(server_origin, code, verifier)


def _new_attempt() -> tuple[str, str, str]:
    """Returns a fresh (state, verifier, S256 challenge) for one login attempt."""
    verifier = new_verifier()
    return new_state(), verifier, s256_challenge(verifier)


def _browser_attempt(
    server_origin: str, deps: LoginDependencies, timeout: float, login_hint: str | None
) -> tuple[str, str] | None:
    """Runs the loopback flow; returns (code, verifier), or None if no browser opened."""
    state, verifier, challenge = _new_attempt()
    with CallbackReceiver(state, server_origin + SUCCESS_PATH) as receiver:
        url = build_login_url(server_origin, state, challenge, receiver.port, login_hint)
        prompt = f"Opening your browser to sign in to {server_origin}.\nIf it does not open, visit:"
        deps.out.write(f"{prompt}\n\n  {url}\n\n")
        if not _launch_browser(deps.open_browser, url):
            deps.out.write("Could not open a browser; switching to manual login.\n")
            return None
        return receiver.wait_for_code(timeout), verifier


def _launch_browser(open_browser: Callable[[str], bool], url: str) -> bool:
    """Opens the browser and reports whether it launched."""
    try:
        return bool(open_browser(url))
    except Exception:  # webbrowser raises for broken BROWSER settings.
        return False


def _manual_attempt(
    server_origin: str, deps: LoginDependencies, login_hint: str | None
) -> tuple[str, str]:
    """Prints the manual-mode URL and returns (pasted code, verifier)."""
    state, verifier, challenge = _new_attempt()
    url = build_login_url(server_origin, state, challenge, None, login_hint)
    deps.out.write(
        f"Open this URL in a browser on any device and sign in:\n\n  {url}\n\n{MANUAL_WARNING}\n"
    )
    code = deps.read_code(MANUAL_CODE_PROMPT).strip()
    if not (1 <= len(code) <= 256 and not any(c.isspace() for c in code)):
        raise AuthError("No valid login code was entered. Run `xmc login` again.")
    return code, verifier
