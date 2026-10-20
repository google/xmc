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

"""XMC Python SDK package.

Names backed by the generated metrics protos (`xmc.buffer`, `xmc.flusher`,
`xmc.types`) are imported lazily (PEP 562), so stdlib-only subpackages such as
`xmc.auth` (the `xmc login` CLI) work without `api.protos` installed.
"""

from __future__ import annotations

import importlib
from typing import TYPE_CHECKING, Any

from xmc.autolog import autolog
from xmc.client import Client, XMCAPIError, XMCError, XMCNetworkError
from xmc.dist_harvest import (
    GetReplicaIDAndSilenceStatus,
    get_replica_id_and_silence_status,
)
from xmc.network import HTTPClient, SendResult
from xmc.resilience import ResilienceState, ResilienceStateMachine

if TYPE_CHECKING:
    from xmc.buffer import DEFAULT_CAPACITY, BoundedMetricsBuffer, MetricTuple
    from xmc.flusher import MetricsFlusher
    from xmc.types import Metric, validate_metric

_LAZY_ATTRIBUTES = {
    "BoundedMetricsBuffer": "xmc.buffer",
    "DEFAULT_CAPACITY": "xmc.buffer",
    "MetricTuple": "xmc.buffer",
    "MetricsFlusher": "xmc.flusher",
    "Metric": "xmc.types",
    "validate_metric": "xmc.types",
}


def __getattr__(name: str) -> Any:
    """Imports proto-backed public names on first access (PEP 562)."""
    module = _LAZY_ATTRIBUTES.get(name)
    if module is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    value = getattr(importlib.import_module(module), name)
    globals()[name] = value
    return value


__version__ = "0.1.0"

__all__ = [
    "autolog",
    "BoundedMetricsBuffer",
    "DEFAULT_CAPACITY",
    "MetricTuple",
    "Client",
    "HTTPClient",
    "MetricsFlusher",
    "Metric",
    "ResilienceState",
    "ResilienceStateMachine",
    "SendResult",
    "XMCError",
    "XMCNetworkError",
    "XMCAPIError",
    "validate_metric",
    "get_replica_id_and_silence_status",
    "GetReplicaIDAndSilenceStatus",
    "__version__",
]

_global_flusher: MetricsFlusher | None = None


def init(*args, **kwargs) -> None:
    """Initializes the global SDK metrics flusher daemon."""
    global _global_flusher
    if _global_flusher is None:
        from xmc.flusher import MetricsFlusher

        _global_flusher = MetricsFlusher(client=Client(*args, **kwargs))


def log_metric(name: str, value: float | int, step: int | None = None) -> None:
    """Logs a metric to the background flusher daemon. Auto-initializes if needed."""
    global _global_flusher
    if _global_flusher is None:
        init()
    assert _global_flusher is not None
    _global_flusher.log_metric(name, value, step)


def log(name: str, value: float | int, step: int | None = None) -> None:
    """Alias for log_metric."""
    log_metric(name, value, step)


def shutdown() -> None:
    """Gracefully shuts down the global SDK metrics flusher."""
    global _global_flusher
    if _global_flusher is not None:
        _global_flusher.shutdown()
        _global_flusher = None


__all__.extend(
    [
        "init",
        "log_metric",
        "log",
        "shutdown",
    ]
)
