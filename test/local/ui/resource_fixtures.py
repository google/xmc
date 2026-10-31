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
"""Deterministic resource-dashboard data for the local UI preview server."""

import base64
import binascii
import json
import re
from typing import Any

GPU_QUEUE = "gpu-cluster-queue"
CPU_QUEUE = "cpu-cluster-queue"
EMPTY_QUEUE = "empty-cluster-queue"
H100 = "nvidia.com/gpu: nvidia-h100"
L4 = "nvidia.com/gpu: nvidia-l4"
AUTHORS = ("alice@example.com", "bob@example.com", "carol@example.com")
STATES = ("RUNNING", "STARTING", "INITIALIZING", "SCHEDULING")
RANGES = ("RANGE_PRESET_LAST_HOUR", "RANGE_PRESET_LAST_DAY", "RANGE_PRESET_LAST_WEEK")
PREDICATE = re.compile(r'\s*(author|status)\s*=\s*("(?:[^"\\]|\\.)*"|[A-Z_]+)\s*')


def _capacity(kind: str, total: int, used: int, headroom: int) -> dict[str, Any]:
    return {
        "resourceType": kind,
        "totalCapacity": total,
        "usedCapacity": used,
        "freeCapacity": max(total - used, 0),
        "freeCapacityWithBorrowing": headroom,
    }


def cluster_queues() -> dict[str, Any]:
    """Return independent quota fixtures, including usage above nominal quota."""
    return {
        "queues": [
            {
                "queueName": GPU_QUEUE,
                "queueCapacity": [_capacity(H100, 16, 24, 8), _capacity(L4, 16, 6, 18)],
            },
            {"queueName": CPU_QUEUE, "queueCapacity": [_capacity("cpu", 128, 32, 96)]},
            {"queueName": EMPTY_QUEUE, "queueCapacity": [_capacity(H100, 8, 0, 8)]},
        ]
    }


def _require_queue(queue: str) -> None:
    if queue not in (GPU_QUEUE, CPU_QUEUE, EMPTY_QUEUE):
        raise KeyError(queue)


def _job(index: int, cpu: bool = False) -> dict[str, Any]:
    state = (
        ("RUNNING", "RUNNING", "SCHEDULING", "INITIALIZING")[index] if cpu else STATES[index // 6]
    )
    kind = "cpu" if cpu else (H100 if index % 2 == 0 else L4)
    count = 16 if cpu else (8 if index % 2 == 0 else 2)
    xid = (234912000 if cpu else 234911122) + index // 2
    return {
        "xid": xid,
        "workUnitId": index % 2 + 1,
        "experimentTitle": f"{'CPU preprocessing' if cpu else 'GPU training'} {index // 2 + 1}",
        "user": AUTHORS[index % len(AUTHORS)],
        "status": state,
        "priority": (100, 50, 0, -10)[index % 4],
        "positionInQueue": index - 17
        if state == "SCHEDULING" and not cpu
        else (-1 if state != "SCHEDULING" else 1),
        "timeInQueueSeconds": (index + 1) * 60,
        "createTime": f"2026-01-01T00:{index:02d}:00Z",
        "requestedResources": [{"resourceType": kind, "count": count}],
        "tags": ["local-preview"],
    }


def _jobs(queue: str) -> list[dict[str, Any]]:
    _require_queue(queue)
    if queue == EMPTY_QUEUE:
        return []
    return [_job(index, queue == CPU_QUEUE) for index in range(4 if queue == CPU_QUEUE else 24)]


def _parse_filter(expression: str) -> dict[str, str]:
    result: dict[str, str] = {}
    remaining = expression.strip()
    while remaining:
        match = PREDICATE.match(remaining)
        if match is None:
            raise ValueError("filter supports author and status equalities joined by AND")
        field, value = match.groups()
        if field in result:
            raise ValueError("filter fields may appear only once")
        if field == "author":
            if not value.startswith('"'):
                raise ValueError("author must be a quoted string")
            value = json.loads(value)
        elif value not in STATES:
            raise ValueError("status must be RUNNING, STARTING, INITIALIZING, or SCHEDULING")
        result[field] = value
        remaining = remaining[match.end() :]
        if remaining:
            delimiter = re.match(r"AND\s+", remaining)
            if delimiter is None or not remaining[delimiter.end() :].strip():
                raise ValueError("filter predicates must be joined by AND")
            remaining = remaining[delimiter.end() :]
    return result


def _sort_jobs(jobs: list[dict[str, Any]], order_by: str) -> list[dict[str, Any]]:
    ordered = sorted(jobs, key=lambda job: (job["workUnitId"], job["xid"]))
    if not order_by.strip():
        return sorted(
            ordered,
            key=lambda job: (
                job["status"] == "SCHEDULING",
                job["positionInQueue"],
                job["createTime"],
            ),
        )
    parts = order_by.split()
    if len(parts) > 2 or parts[0] not in ("author", "status", "priority"):
        raise ValueError("orderBy must name author, status, or priority")
    direction = parts[1].lower() if len(parts) == 2 else "asc"
    if direction not in ("asc", "desc"):
        raise ValueError("orderBy direction must be asc or desc")
    field = "user" if parts[0] == "author" else parts[0]
    return sorted(
        ordered,
        key=lambda job: STATES.index(job[field]) if field == "status" else job[field],
        reverse=direction == "desc",
    )


def _offset(token: str) -> int:
    if not token:
        return 0
    try:
        decoded = base64.b64decode(
            token + "=" * (-len(token) % 4), altchars=b"-_", validate=True
        ).decode("ascii")
        offset = int(decoded)
    except (ValueError, UnicodeError, binascii.Error) as error:
        raise ValueError("invalid pageToken") from error
    if offset < 0 or not decoded.isdecimal():
        raise ValueError("invalid pageToken")
    return offset


def queue_jobs(queue: str, params: dict[str, list[str]]) -> dict[str, Any]:
    """Filter and sort the whole queue before returning an offset-paginated page."""
    if any(len(values) != 1 for values in params.values()):
        raise ValueError("query parameters must appear only once")
    size = int(params.get("pageSize", ["10"])[0])
    if size < 0:
        raise ValueError("pageSize cannot be negative")
    size = min(size or 10, 100)
    offset = _offset(params.get("pageToken", [""])[0])
    filters = _parse_filter(params.get("filter", [""])[0])
    jobs = [
        job
        for job in _jobs(queue)
        if all(job["user" if key == "author" else key] == value for key, value in filters.items())
    ]
    ordered = _sort_jobs(jobs, params.get("orderBy", [""])[0])
    end = offset + size
    token = base64.urlsafe_b64encode(str(end).encode()).decode() if end < len(ordered) else ""
    return {"jobs": ordered[offset:end], "totalQueueSize": len(ordered), "nextPageToken": token}


def submit_queue(queue: str) -> dict[str, str]:
    """Return the local submission queue associated with a preview cluster queue."""
    _require_queue(queue)
    return {"submitQueue": queue.replace("cluster-queue", "local-queue")}


def secondary_data(body: Any) -> dict[str, Any]:
    """Return predictability fixtures for only the requested queues and time range."""
    if not isinstance(body, dict) or not isinstance(body.get("queueNames", []), list):
        raise ValueError("queueNames must be an array")
    period = body.get("range", RANGES[0])
    if type(period) is int and 0 <= period <= 3:
        period = RANGES[max(period - 1, 0)]
    if period == "RANGE_PRESET_UNSPECIFIED":
        period = RANGES[0]
    if period not in RANGES:
        raise ValueError("unsupported range")
    result = {}
    for queue in body.get("queueNames", []):
        if not isinstance(queue, str):
            raise ValueError("queueNames must contain strings")
        _require_queue(queue)
        pending = [job for job in _jobs(queue) if job["status"] == "SCHEDULING"]
        result[queue] = {"queuePredictability": _predictability(pending, period)}
    return {"queuePredictability": result}


def _predictability(pending: list[dict[str, Any]], period: str) -> list[dict[str, Any]]:
    resources: dict[str, int] = {}
    for job in pending:
        for resource in job["requestedResources"]:
            kind = resource["resourceType"]
            resources[kind] = resources.get(kind, 0) + resource["count"]
    return [
        {
            "pendingResourceRequests": {"resourceType": kind, "count": count},
            "resourceVelocity": {"resourceType": kind, "velocity": count / 2},
            "pendingJobs": len(pending),
            "jobVelocity": len(pending) / 2,
            "velocityRange": period,
        }
        for kind, count in sorted(resources.items())
    ]
