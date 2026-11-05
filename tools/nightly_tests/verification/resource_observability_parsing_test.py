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

"""Unit tests for resource_observability_parsing module."""

from __future__ import annotations

import unittest

from tools.nightly_tests.verification import resource_observability_parsing as rod_parsing


class ResourceObservabilityParsingTest(unittest.TestCase):
    """Test suite for pure Resource Observability Dashboard parsing functions."""

    def test_resolve_cluster_queue(self) -> None:
        cases: list[tuple[str | None, str]] = [
            (None, "cpu-cluster-queue"),
            ("", "cpu-cluster-queue"),
            ("   ", "cpu-cluster-queue"),
            ("cpu-queue", "cpu-cluster-queue"),
            ("gpu-queue", "gpu-cluster-queue"),
            ("multikueue-cpu-queue", "multikueue-cpu-cluster-queue"),
            ("gpu-cluster-queue", "gpu-cluster-queue"),
            ("team-queue-cpu-queue", "team-queue-cpu-cluster-queue"),
            ("custom-pool", "custom-pool"),
        ]
        for raw_queue, expected in cases:
            with self.subTest(raw_queue=raw_queue):
                self.assertEqual(rod_parsing.resolve_cluster_queue(raw_queue), expected)

    def test_parse_velocity_rate(self) -> None:
        cases: list[tuple[str, float]] = [
            ("0.86 jobs/hr", 0.86),
            ("~1.34 cpu/hr", 1.34),
            ("  ~12 nvidia-h100/hr  ", 12.0),
            ("0 jobs/hr", 0.0),
            ("~0 cpu/hr", 0.0),
            ("-0.5 jobs/hr", -0.5),
            ("~-1.2 cpu/hr", -1.2),
            ("N/A", 0.0),
            ("N/A (7d)", 0.0),
            ("No data for h100", 0.0),
            ("1.2.3 jobs/hr", 0.0),
            ("", 0.0),
        ]
        for text, expected in cases:
            with self.subTest(text=text):
                self.assertEqual(rod_parsing.parse_velocity_rate(text), expected)

    def test_is_valid_queue_position(self) -> None:
        for valid_pos in ("N/A - running", "  N/A - running  ", "1", "2", "42"):
            with self.subTest(valid_pos=valid_pos):
                self.assertTrue(rod_parsing.is_valid_queue_position(valid_pos))

        for invalid_pos in (
            "",
            "   ",
            "-",
            "N/A",
            "n/a - running",
            "N/A - pending",
            "0",
            "00",
            "-1",
            "1.5",
            "Pending",
            "\u00b2",
        ):
            with self.subTest(invalid_pos=invalid_pos):
                self.assertFalse(rod_parsing.is_valid_queue_position(invalid_pos))


if __name__ == "__main__":
    unittest.main()
