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
"""Unit tests for `xm_cloud.cli.terminal_text`."""

from __future__ import annotations

import io
import unittest

from xm_cloud.cli import terminal_text


def _strict_stream(encoding: str) -> tuple[io.TextIOWrapper, io.BytesIO]:
    """Returns a stream that, like a real `sys.stdout`, raises on unencodable text."""
    buffer = io.BytesIO()
    return io.TextIOWrapper(buffer, encoding=encoding, errors="strict"), buffer


class TerminalTextTest(unittest.TestCase):
    """Unit tests for `xm_cloud.cli.terminal_text`."""

    def test_escape_nonprintable_escapes_terminal_controls(self) -> None:
        cases = [
            ("\x1b[31mred", "\\x1b[31mred"),  # C0 ESC (CSI).
            ("\x9b31m", "\\x9b31m"),  # C1 8-bit CSI.
            ("a\rb", "a\\rb"),  # Carriage return overwrites the line.
            ("\x7f", "\\x7f"),
            ("\u202emoc.evil", "\\u202emoc.evil"),  # Bidi override.
            ("a\u200bb", "a\\u200bb"),  # Zero-width space.
            ("x\udcffy", "x\\udcffy"),  # Undecodable environment byte.
        ]
        for raw, expected in cases:
            with self.subTest(raw=ascii(raw)):
                self.assertEqual(terminal_text.escape_nonprintable(raw), expected)

    def test_escape_nonprintable_keeps_newlines_and_printable_unicode(self) -> None:
        text = "Logged in to https://bücher.example as u@example.com.\n"

        self.assertEqual(terminal_text.escape_nonprintable(text), text)

    def test_write_survives_a_strict_utf8_stream(self) -> None:
        stream, buffer = _strict_stream("utf-8")

        terminal_text.write(stream, "Error: /tmp/\udcff.json\n")

        stream.flush()
        self.assertEqual(buffer.getvalue(), b"Error: /tmp/\\udcff.json\n")

    def test_write_escapes_characters_the_stream_cannot_encode(self) -> None:
        stream, buffer = _strict_stream("ascii")

        terminal_text.write(stream, "https://bücher.example\n")

        stream.flush()
        self.assertEqual(buffer.getvalue(), b"https://b\\xfccher.example\n")

    def test_write_to_a_stream_without_an_encoding_uses_utf8(self) -> None:
        stream = io.StringIO()

        terminal_text.write(stream, "bücher \x1b\n")

        self.assertEqual(stream.getvalue(), "bücher \\x1b\n")


if __name__ == "__main__":
    unittest.main()
