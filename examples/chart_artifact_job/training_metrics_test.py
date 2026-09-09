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

"""Unit tests for the randomized training curve sampler."""

import random
import unittest

from training_metrics import (
    ACCURACY_METRIC,
    LEARNING_RATE_METRIC,
    LOSS_METRIC,
    METRIC_NAMES,
    sample_step_metrics,
)

_TOTAL_STEPS = 20


class SampleStepMetricsTest(unittest.TestCase):
    """Covers the value ranges, determinism and guard clauses of the sampler."""

    def test_returns_every_declared_metric(self):
        sample = sample_step_metrics(1, _TOTAL_STEPS, random.Random(7))

        self.assertCountEqual(METRIC_NAMES, sample.keys())

    def test_clamps_values_into_their_valid_ranges(self):
        rng = random.Random(11)

        for step in range(1, _TOTAL_STEPS + 1):
            with self.subTest(step=step):
                sample = sample_step_metrics(step, _TOTAL_STEPS, rng)

                self.assertGreaterEqual(sample[LOSS_METRIC], 0.05)
                self.assertGreaterEqual(sample[ACCURACY_METRIC], 0.0)
                self.assertLessEqual(sample[ACCURACY_METRIC], 1.0)
                self.assertGreater(sample[LEARNING_RATE_METRIC], 0.0)

    def test_learning_rate_decays_monotonically(self):
        rng = random.Random(3)
        rates = [
            sample_step_metrics(step, _TOTAL_STEPS, rng)[LEARNING_RATE_METRIC]
            for step in range(1, _TOTAL_STEPS + 1)
        ]

        self.assertEqual(rates, sorted(rates, reverse=True))

    def test_loss_trends_down_and_accuracy_trends_up_across_the_run(self):
        rng = random.Random(5)
        samples = [
            sample_step_metrics(step, _TOTAL_STEPS, rng) for step in range(1, _TOTAL_STEPS + 1)
        ]

        self.assertLess(samples[-1][LOSS_METRIC], samples[0][LOSS_METRIC])
        self.assertGreater(samples[-1][ACCURACY_METRIC], samples[0][ACCURACY_METRIC])

    def test_same_seed_reproduces_identical_samples(self):
        first = sample_step_metrics(4, _TOTAL_STEPS, random.Random(42))
        second = sample_step_metrics(4, _TOTAL_STEPS, random.Random(42))

        self.assertEqual(first, second)

    def test_different_seeds_produce_different_samples(self):
        first = sample_step_metrics(4, _TOTAL_STEPS, random.Random(1))
        second = sample_step_metrics(4, _TOTAL_STEPS, random.Random(2))

        self.assertNotEqual(first[LOSS_METRIC], second[LOSS_METRIC])

    def test_rejects_non_positive_total_steps(self):
        with self.assertRaises(ValueError):
            sample_step_metrics(1, 0, random.Random(1))

    def test_rejects_step_outside_the_run(self):
        for step in (0, _TOTAL_STEPS + 1):
            with self.subTest(step=step):
                with self.assertRaises(ValueError):
                    sample_step_metrics(step, _TOTAL_STEPS, random.Random(1))


if __name__ == "__main__":
    unittest.main()
