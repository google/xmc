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

"""XMC Keras Autologging Integration Subpackage."""

import importlib.metadata
from typing import Any

import xmc

MIN_KERAS_VERSION = "3.0.0"

try:
    import keras
except ImportError as exc:
    raise ImportError(
        "Keras is required to use XmcKerasCallback. Please install Keras >= 3.0.0 "
        "via 'pip install xmc[keras]' or 'pip install keras>=3.0.0'."
    ) from exc


def _parse_version(version_str: str) -> tuple[int, ...]:
    """Parses a version string into an integer tuple for comparison."""
    parts = []
    for segment in version_str.split(".")[:3]:
        digits = "".join(char for char in segment if char.isdigit())
        parts.append(int(digits) if digits else 0)
    return tuple(parts)


def _validate_keras_version() -> None:
    """Validates Keras version >= 3.0.0, failing fast if incompatible."""
    version_str = getattr(keras, "__version__", None)
    if version_str is None:
        try:
            version_str = importlib.metadata.version("keras")
        except Exception:
            version_str = "0.0.0"

    if _parse_version(version_str) < _parse_version(MIN_KERAS_VERSION):
        raise ImportError(
            f"Keras version >= {MIN_KERAS_VERSION} is required for XmcKerasCallback, "
            f"found version {version_str}. Please upgrade via 'pip install --upgrade xmc[keras]' "
            f"or 'pip install --upgrade keras>=3.0.0'."
        )


_validate_keras_version()


class XmcKerasCallback(keras.callbacks.Callback):
    """Keras Callback for automatic metric logging to XMC SDK."""

    def __init__(
        self,
        prefix: str = "",
        log_batch_frequency: int = 1,
        client: Any | None = None,
    ) -> None:
        """Initializes the XMC Keras callback.

        Args:
            prefix: Optional string prefix to prepend to metric names.
            log_batch_frequency: Interval of training batches between metric logging calls.
            client: Optional explicit Client instance. Defaults to global xmc flusher.
        """
        super().__init__()
        self.prefix = prefix
        self.log_batch_frequency = max(1, log_batch_frequency)
        self.client = client
        self._batch_count = 0

    def _log(self, name: str, value: float | int, step: int | None = None) -> None:
        """Logs a single metric via the configured client or global flusher."""
        metric_name = f"{self.prefix}{name}" if self.prefix else name
        if self.client is not None:
            self.client.log_metric(metric_name, value, step=step)
        else:
            xmc.log_metric(metric_name, value, step=step)

    def _get_global_step(self) -> int | None:
        """Extracts current global optimizer step from model if available."""
        if not hasattr(self, "model") or self.model is None:
            return None
        if not hasattr(self.model, "optimizer") or self.model.optimizer is None:
            return None

        opt = self.model.optimizer
        if not hasattr(opt, "iterations"):
            return None

        iters = opt.iterations
        if hasattr(iters, "item") and callable(iters.item):
            try:
                res = iters.item()
                if isinstance(res, (int, float)):
                    return int(res)
            except Exception:
                pass

        if callable(getattr(iters, "numpy", None)):
            try:
                return int(iters.numpy())
            except Exception:
                pass

        try:
            return int(iters)
        except Exception:
            return None

    @staticmethod
    def _extract_scalar(val: Any) -> float | int | None:
        """Extracts a python float or int scalar from various scalar/tensor types."""
        if isinstance(val, (int, float)):
            return val

        if hasattr(val, "item") and callable(val.item):
            try:
                res = val.item()
                if isinstance(res, (int, float)):
                    return res
            except Exception:
                pass

        if hasattr(val, "numpy") and callable(val.numpy):
            try:
                n_val = val.numpy()
                if isinstance(n_val, (int, float)):
                    return n_val
                if hasattr(n_val, "item") and callable(n_val.item):
                    res = n_val.item()
                    if isinstance(res, (int, float)):
                        return res
                return float(n_val)
            except Exception:
                pass

        try:
            return float(val)
        except Exception:
            return None

    def on_epoch_begin(self, epoch: int, logs: dict[str, Any] | None = None) -> None:
        """Keras callback hook fired at the start of each training epoch."""
        self._batch_count = 0

    def on_train_batch_end(self, batch: int, logs: dict[str, Any] | None = None) -> None:
        """Keras callback hook fired at the end of each training batch."""
        self._batch_count += 1
        if self._batch_count % self.log_batch_frequency != 0 or not logs:
            return

        step = self._get_global_step()
        for key, val in logs.items():
            scalar_val = self._extract_scalar(val)
            if scalar_val is not None:
                self._log(key, scalar_val, step=step)

    def on_epoch_end(self, epoch: int, logs: dict[str, Any] | None = None) -> None:
        """Keras callback hook fired at the end of each training epoch."""
        if not logs:
            return

        step = self._get_global_step()
        for key, val in logs.items():
            scalar_val = self._extract_scalar(val)
            if scalar_val is not None:
                self._log(key, scalar_val, step=step)
