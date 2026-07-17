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

"""XMC Metrics data model and pre-validation logic using proto."""

import math
import re

# Import the generated proto. The path depends on the bazel package structure.
from api.protos.metrics import metrics_pb2

# Re-export the Metric class so it is available at xmc.types.Metric
Metric = metrics_pb2.Metric

_MAX_CACHED_METRIC_NAMES: int = 10000
_METRIC_NAME_REGEX: re.Pattern[str] = re.compile(r"^[a-zA-Z0-9_\.\-\/]{1,255}$")
_validated_metric_names: set[str] = set()


def validate_metric(metric: metrics_pb2.Metric) -> None:
    """Validates metric fields according to XMC metrics rules.

    Args:
        metric: The Metric proto instance to validate.

    Raises:
        ValueError: If metric_name, metric_value, or step is invalid.
    """
    global _validated_metric_names

    metric_name = metric.metric_name
    if metric_name not in _validated_metric_names:
        if not _METRIC_NAME_REGEX.match(metric_name):
            raise ValueError(
                f"Invalid metric_name {metric_name!r}: must match regex"
                " ^[a-zA-Z0-9_\\.\\-\\/]{1,255}$"
            )
        if len(_validated_metric_names) >= _MAX_CACHED_METRIC_NAMES:
            _validated_metric_names.clear()
        _validated_metric_names.add(metric_name)

    if not math.isfinite(metric.metric_value):
        raise ValueError(f"Invalid metric_value {metric.metric_value!r}: must be a finite number")

    if metric.step < 0:
        raise ValueError(f"Invalid step {metric.step!r}: must be >= 0")


# Add a validation method to the generated proto class dynamically for convenience,
# matching the previous SDK contract.
metrics_pb2.Metric.validate = validate_metric
