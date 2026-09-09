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

"""Randomized training curves shared by the chart artifact worker and launcher.

The worker emits these metrics into ClickHouse and the launcher references the exact
same names in the Chart it provisions, so both import this module to stay in sync.
"""

from __future__ import annotations

import math
import random

LOSS_METRIC = "chart_demo/loss"
ACCURACY_METRIC = "chart_demo/accuracy"
LEARNING_RATE_METRIC = "chart_demo/learning_rate"

# Ordering is significant: it drives both the plot order and the artifact metric list.
METRIC_NAMES: tuple[str, ...] = (LOSS_METRIC, ACCURACY_METRIC, LEARNING_RATE_METRIC)

_INITIAL_LOSS = 2.5
_FLOOR_LOSS = 0.05
_PEAK_ACCURACY = 0.97
_INITIAL_LEARNING_RATE = 0.01
_FLOOR_LEARNING_RATE = 0.0001
_DECAY_RATE = 3.0
_LOSS_NOISE_STDDEV = 0.08
_ACCURACY_NOISE_STDDEV = 0.02


def sample_step_metrics(step: int, total_steps: int, rng: random.Random) -> dict[str, float]:
    """Samples one noisy training observation for a single step.

    Loss decays exponentially towards a floor and accuracy rises towards a ceiling, both
    perturbed by Gaussian noise so successive runs produce visibly different curves. The
    learning rate follows a noise-free cosine annealing schedule down to a nonzero floor.

    Args:
        step: 1-based step index being sampled.
        total_steps: Total number of steps in the run, used to normalise progress.
        rng: Random source; seed it to make a run reproducible.

    Returns:
        Mapping of metric name to sampled value, keyed by the constants in this module.

    Raises:
        ValueError: If total_steps is not positive or step falls outside 1..total_steps.
    """
    if total_steps < 1:
        raise ValueError(f"total_steps must be positive, got {total_steps}")
    if step < 1 or step > total_steps:
        raise ValueError(f"step {step} must fall within 1..{total_steps}")

    progress = step / total_steps
    decay = math.exp(-_DECAY_RATE * progress)
    loss = _FLOOR_LOSS + (_INITIAL_LOSS - _FLOOR_LOSS) * decay + rng.gauss(0.0, _LOSS_NOISE_STDDEV)
    accuracy = _PEAK_ACCURACY * (1.0 - decay) + rng.gauss(0.0, _ACCURACY_NOISE_STDDEV)
    cosine_decay = 0.5 * (1.0 + math.cos(math.pi * progress))
    learning_rate = (
        _FLOOR_LEARNING_RATE + (_INITIAL_LEARNING_RATE - _FLOOR_LEARNING_RATE) * cosine_decay
    )

    return {
        LOSS_METRIC: max(loss, _FLOOR_LOSS),
        ACCURACY_METRIC: min(max(accuracy, 0.0), 1.0),
        LEARNING_RATE_METRIC: learning_rate,
    }
