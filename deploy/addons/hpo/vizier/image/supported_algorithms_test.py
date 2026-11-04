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

"""Tests for supported_algorithms."""

import unittest

import supported_algorithms


class CheckAlgorithmTest(unittest.TestCase):
    def test_accepts_supported_algorithms(self):
        for algorithm in ("RANDOM_SEARCH", "QUASI_RANDOM_SEARCH", "GRID_SEARCH"):
            with self.subTest(algorithm=algorithm):
                supported_algorithms.check_algorithm(algorithm)

    def test_rejects_unset_and_gaussian_process_algorithms(self):
        for algorithm in (
            "",
            "ALGORITHM_UNSPECIFIED",
            "DEFAULT",
            "GP_UCB_PE",
            "GAUSSIAN_PROCESS_BANDIT",
            "random_search",
        ):
            with (
                self.subTest(algorithm=algorithm),
                self.assertRaisesRegex(
                    supported_algorithms.UnsupportedAlgorithmError, "is not supported"
                ),
            ):
                supported_algorithms.check_algorithm(algorithm)

    def test_names_unset_algorithm_and_alternatives(self):
        with self.assertRaises(supported_algorithms.UnsupportedAlgorithmError) as raised:
            supported_algorithms.check_algorithm("")

        self.assertIn("'ALGORITHM_UNSPECIFIED'", str(raised.exception))
        self.assertIn("RANDOM_SEARCH, QUASI_RANDOM_SEARCH, GRID_SEARCH", str(raised.exception))

    def test_error_is_a_value_error(self):
        self.assertTrue(issubclass(supported_algorithms.UnsupportedAlgorithmError, ValueError))


if __name__ == "__main__":
    unittest.main()
