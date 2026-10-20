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

"""Unit tests for xmc.auth.browser_detection."""

import unittest

from xmc.auth import browser_detection


class BrowserLikelyAvailableTest(unittest.TestCase):
    """Tests browser availability heuristics."""

    def test_cases(self) -> None:
        """SSH and display-less Unix sessions fall back to manual login."""
        cases = [
            ({"DISPLAY": ":0"}, "linux", True),
            ({"WAYLAND_DISPLAY": "wayland-0"}, "linux", True),
            ({}, "linux", False),
            ({"DISPLAY": ""}, "linux", False),
            ({}, "freebsd14", False),
            ({}, "darwin", True),
            ({}, "win32", True),
            ({}, "msys", True),
            ({"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, "linux", False),
            ({"SSH_TTY": "/dev/pts/1"}, "darwin", False),
        ]
        for env, platform, want in cases:
            with self.subTest(env=env, platform=platform):
                self.assertEqual(browser_detection.browser_likely_available(env, platform), want)


if __name__ == "__main__":
    unittest.main()
