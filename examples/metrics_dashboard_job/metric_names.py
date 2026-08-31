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

"""Metric names shared by the worker that emits them and the launcher that charts them.

A dashboard is only useful when the names in its plot specs are the ones the worker
actually logged. A mismatch does not fail anywhere: the chart is created, the query runs
and the plot renders empty forever, which is a slow thing to debug from the UI. Declaring
the names once in a module both sides import removes that failure mode, so this file is
copied into the worker image as well as imported by the launch script.
"""

TRAIN_LOSS = "train/loss"
TRAIN_ACCURACY = "train/accuracy"
EVAL_LOSS = "eval/loss"
EVAL_ACCURACY = "eval/accuracy"
STEP_TIME_MS = "perf/step_time_ms"

# Every name this example emits, in the order a reader most likely wants them.
ALL = (
    TRAIN_LOSS,
    TRAIN_ACCURACY,
    EVAL_LOSS,
    EVAL_ACCURACY,
    STEP_TIME_MS,
)
