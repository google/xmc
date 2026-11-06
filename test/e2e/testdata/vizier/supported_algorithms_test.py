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


class UnsupportedAlgorithmErrorTest(unittest.TestCase):
    def test_accepts_supported_algorithms(self):
        for algorithm in ("RANDOM_SEARCH", "QUASI_RANDOM_SEARCH", "GRID_SEARCH"):
            with self.subTest(algorithm=algorithm):
                self.assertIsNone(supported_algorithms.unsupported_algorithm_error(algorithm))

    def test_rejects_unset_gaussian_process_and_miscased_algorithms(self):
        for algorithm in (
            "",
            "ALGORITHM_UNSPECIFIED",
            "DEFAULT",
            "GP_UCB_PE",
            "GAUSSIAN_PROCESS_BANDIT",
            "random_search",
            " RANDOM_SEARCH",
        ):
            with self.subTest(algorithm=algorithm):
                error = supported_algorithms.unsupported_algorithm_error(algorithm)
                self.assertIsNotNone(error)
                self.assertIn("is not supported", error)

    def test_names_unset_algorithm_and_alternatives(self):
        error = supported_algorithms.unsupported_algorithm_error("")

        self.assertEqual(
            error,
            "algorithm 'ALGORITHM_UNSPECIFIED' is not supported by this Vizier fixture; "
            "use one of RANDOM_SEARCH, QUASI_RANDOM_SEARCH, GRID_SEARCH",
        )


if __name__ == "__main__":
    unittest.main()
