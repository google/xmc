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

"""XMC Metrics data model, validation, and batch payload serialization."""

import functools
import math
import re
from typing import Any

# Import the generated proto. The path depends on the bazel package structure.
from api.protos.metrics import metrics_pb2

# Re-export the Metric class so it is available at xmc.types.Metric
Metric = metrics_pb2.Metric

_METRIC_NAME_REGEX: re.Pattern[str] = re.compile(r"^[a-zA-Z0-9_\.\-\/]{1,255}$")


@functools.lru_cache(maxsize=10000)
def is_valid_metric_name(name: str) -> bool:
    """Checks whether a metric name matches the allowed character set and length."""
    return bool(_METRIC_NAME_REGEX.match(name))


def validate_metric(metric: metrics_pb2.Metric) -> None:
    """Validates metric fields according to XMC metrics rules.

    Args:
        metric: The Metric proto instance to validate.

    Raises:
        ValueError: If metric_name, metric_value, or step is invalid.
    """
    if not is_valid_metric_name(metric.metric_name):
        raise ValueError(
            f"Invalid metric_name {metric.metric_name!r}: must match regex"
            " ^[a-zA-Z0-9_\\.\\-\\/]{1,255}$"
        )

    if not math.isfinite(metric.metric_value):
        raise ValueError(f"Invalid metric_value {metric.metric_value!r}: must be a finite number")

    if metric.step < 0:
        raise ValueError(f"Invalid step {metric.step!r}: must be >= 0")


metrics_pb2.Metric.validate = validate_metric


def format_metric_batch_payload(
    items: list[tuple[str, float, int, float]],
    xid: str,
    work_unit_id: str,
    execution_id: str,
    replica_id: str,
) -> list[dict[str, Any]]:
    """Formats metric tuples into JSON payload list matching XMCMetricsBatch schema.

    Instantiates Metric proto objects per item to validate schema compliance
    and converts float epoch timestamp to microsecond int64.
    """

    def _to_microsecond_ts(ts: float | int) -> int:
        val = float(ts)
        if val > 1e11:
            return int(val)
        return int(round(val * 1_000_000))

    def _to_int_id(val: str | int | None) -> int:
        if not val:
            return 0
        try:
            return int(val)
        except ValueError:
            return 0

    payload = []
    for name, val, step, ts in items:
        m = Metric(
            xid=_to_int_id(xid),
            work_unit_id=_to_int_id(work_unit_id),
            execution_id=_to_int_id(execution_id),
            replica_id=replica_id,
            metric_name=name,
            metric_value=float(val),
            step=int(step),
            timestamp=_to_microsecond_ts(ts),
        )
        payload.append(
            {
                "xid": xid,
                "work_unit_id": work_unit_id,
                "execution_id": execution_id,
                "replica_id": m.replica_id,
                "metric_name": m.metric_name,
                "metric_value": m.metric_value,
                "step": m.step,
                "timestamp": m.timestamp,
            }
        )
    return payload
