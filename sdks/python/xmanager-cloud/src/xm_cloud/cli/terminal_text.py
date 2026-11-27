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
"""Writes untrusted text to `xmc` output streams safely.

Messages can embed values from the environment (paths, URLs), the credentials
file, or a server response. They must neither inject terminal control sequences
nor crash a strict-encoding stream (for example a UTF-8 `sys.stdout` given the
lone surrogates that stand for undecodable bytes in `os.environ`).
"""

from __future__ import annotations

from typing import TextIO


def escape_nonprintable(text: str) -> str:
    """Replaces every non-printable character other than a newline with its escape.

    Non-printable characters are C0/C1 control characters (including ESC and
    carriage return), Unicode format characters such as bidi overrides, and lone
    surrogates.

    Args:
        text: Text that may contain untrusted characters.

    Returns:
        `text` with each such character replaced by its Python escape, such as
        `\\x1b`, `\\u202e`, or `\\udcff`.
    """
    return "".join(ch if ch.isprintable() or ch == "\n" else ascii(ch)[1:-1] for ch in text)


def write(stream: TextIO, text: str) -> None:
    """Writes `text` to `stream` with non-printable and unencodable characters escaped.

    Args:
        stream: Output stream, such as `sys.stdout` or an `io.StringIO`.
        text: Text that may contain untrusted characters.
    """
    safe = escape_nonprintable(text)
    encoding = getattr(stream, "encoding", None) or "utf-8"
    stream.write(safe.encode(encoding, "backslashreplace").decode(encoding))
