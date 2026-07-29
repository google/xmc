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

"""Unit tests for XMC integrations common utilities."""

import unittest
from unittest.mock import MagicMock

from xmc.integrations.common import extract_scalar, parse_version


class CommonUtilsTest(unittest.TestCase):
    """Test suite for xmc.integrations.common."""

    def test_parse_version_formatting(self) -> None:
        """Verifies parse_version handles standard and short version strings."""
        self.assertEqual(parse_version("3.0.0"), (3, 0, 0))
        self.assertEqual(parse_version("2.1.0-rc1"), (2, 1, 0))
        self.assertEqual(parse_version("2.0"), (2, 0, 0))
        self.assertEqual(parse_version("2"), (2, 0, 0))
        self.assertEqual(parse_version(""), (0, 0, 0))

    def test_parse_version_comparisons(self) -> None:
        """Verifies tuple comparison behavior across version formats."""
        self.assertTrue(parse_version("2.0") >= parse_version("2.0.0"))
        self.assertTrue(parse_version("2.1.0") > parse_version("2.0"))
        self.assertTrue(parse_version("1.14.0") < parse_version("2.0.0"))

    def test_extract_scalar_types(self) -> None:
        """Verifies extract_scalar handles primitives, tensors, numpy, and unsupported types."""
        self.assertEqual(extract_scalar(42), 42)
        self.assertEqual(extract_scalar(3.14159), 3.14159)

        item_mock = MagicMock()
        item_mock.item.return_value = 2.718
        self.assertEqual(extract_scalar(item_mock), 2.718)

        np_mock = MagicMock()
        np_mock.numpy.return_value = 1.414
        self.assertEqual(extract_scalar(np_mock), 1.414)

        np_item_mock = MagicMock()
        sub_mock = MagicMock()
        sub_mock.item.return_value = 9.99
        np_item_mock.numpy.return_value = sub_mock
        self.assertEqual(extract_scalar(np_item_mock), 9.99)

        self.assertIsNone(extract_scalar("invalid_string"))
        self.assertIsNone(extract_scalar([1, 2, 3]))


if __name__ == "__main__":
    unittest.main()
