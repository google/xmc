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

"""Pure parsing and normalization logic for Resource Observability Dashboard UI checks."""

from __future__ import annotations

import re

EXPECTED_CLUSTER_QUEUES: tuple[str, ...] = (
    "cpu-cluster-queue",
    "gpu-cluster-queue",
    "tpu-cluster-queue",
)
EXPECTED_RUNNING_POS: str = "N/A - running"

_VELOCITY_RATE_RE = re.compile(r"^\s*~?\s*([+-]?[0-9]+(?:\.[0-9]+)?)(?=\s|/|$)")
_POSITIVE_QUEUE_POS_RE = re.compile(r"[1-9][0-9]*")


def resolve_cluster_queue(expected_queue: str | None = None) -> str:
    """Resolves a submit queue or cluster queue name to its rendered cluster queue title.

    Args:
        expected_queue: Optional submit queue (e.g. ``cpu-queue``) or cluster queue name.

    Returns:
        The normalized cluster queue name, defaulting to ``cpu-cluster-queue``.
    """
    cq = (expected_queue or "").strip() or EXPECTED_CLUSTER_QUEUES[0]
    if cq.endswith("-cluster-queue") or not cq.endswith("-queue"):
        return cq
    return f"{cq.removesuffix('-queue')}-cluster-queue"


def parse_velocity_rate(metric_text: str) -> float:
    """Extracts the leading numeric velocity rate from a ROD predictability metric string.

    Args:
        metric_text: Rendered metric text (e.g. ``0.86 jobs/hr`` or ``~1.34 cpu/hr``).

    Returns:
        The parsed numeric rate, or ``0.0`` when the text is non-numeric or malformed.
    """
    match = _VELOCITY_RATE_RE.match(metric_text)
    if not match:
        return 0.0
    return float(match.group(1))


def is_valid_queue_position(position_text: str) -> bool:
    """Checks whether a rendered queue table position cell represents a running or queued job.

    Args:
        position_text: Text extracted from a ``.position-cell`` cell in the queue jobs table.

    Returns:
        ``True`` if ``position_text`` is ``N/A - running`` or a 1-based positive ASCII integer.
    """
    pos = position_text.strip()
    return pos == EXPECTED_RUNNING_POS or bool(_POSITIVE_QUEUE_POS_RE.fullmatch(pos))
