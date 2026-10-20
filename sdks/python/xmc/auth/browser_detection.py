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

"""Detects whether `xmc login` can open a local browser."""

from __future__ import annotations

import sys
from collections.abc import Mapping

_DESKTOP_PLATFORMS = ("darwin", "win32", "cygwin")


def browser_likely_available(env: Mapping[str, str], platform: str = sys.platform) -> bool:
    """Guesses whether a graphical browser can be opened on this machine.

    Over SSH a browser would open on the remote host (or a text browser would
    take over the terminal), so the CLI falls back to manual login there.

    Args:
        env: Environment variables to consult.
        platform: `sys.platform` value, injectable for tests.

    Returns:
        False for SSH sessions and for Unix sessions without X11 or Wayland.
    """
    if env.get("SSH_CONNECTION") or env.get("SSH_TTY"):
        return False
    if platform.startswith(_DESKTOP_PLATFORMS):
        return True
    return bool(env.get("DISPLAY") or env.get("WAYLAND_DISPLAY"))
