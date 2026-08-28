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

"""XMC PyTorch Lightning Autologging Integration Subpackage."""

import importlib.metadata
import os
from typing import Any

import xmc
from xmc.integrations.common import extract_scalar, parse_version

MIN_LIGHTNING_VERSION = "2.0.0"

try:
    import lightning.pytorch as pl
    from lightning.pytorch.loggers import Logger
    from lightning.pytorch.utilities import rank_zero_only
except ImportError:
    try:
        import pytorch_lightning as pl
        from pytorch_lightning.loggers import Logger
        from pytorch_lightning.utilities import rank_zero_only
    except ImportError as exc:
        raise ImportError(
            "PyTorch Lightning is required to use XmcLogger. Please install "
            "pytorch-lightning>=2.0.0 via 'pip install xmc[lightning]' or "
            "'pip install pytorch-lightning>=2.0.0'."
        ) from exc


def _validate_lightning_version() -> None:
    """Validates PyTorch Lightning version >= 2.0.0, failing fast if incompatible."""
    version_str = getattr(pl, "__version__", None)
    if version_str is None:
        try:
            version_str = importlib.metadata.version("pytorch-lightning")
        except Exception:
            try:
                version_str = importlib.metadata.version("lightning")
            except Exception:
                version_str = "0.0.0"

    if parse_version(version_str) < parse_version(MIN_LIGHTNING_VERSION):
        raise ImportError(
            f"PyTorch Lightning version >= {MIN_LIGHTNING_VERSION} is required for XmcLogger, "
            f"found version {version_str}. Please upgrade via 'pip install --upgrade xmc[lightning]' "
            f"or 'pip install --upgrade pytorch-lightning>=2.0.0'."
        )


_validate_lightning_version()


class XmcLogger(Logger):  # type: ignore[misc]
    """PyTorch Lightning Logger for automatic metric logging to XMC SDK."""

    def __init__(
        self,
        name: str = "xmc",
        version: str | None = None,
        prefix: str = "",
        client: Any | None = None,
        execution_id: str | None = None,
    ) -> None:
        """Initializes the XMC PyTorch Lightning logger.

        Args:
            name: String name for the logger experiment. Defaults to 'xmc'.
            version: Optional experiment version string. Defaults to execution_id.
            prefix: Optional string prefix to prepend to metric names.
            client: Optional explicit Client instance. Defaults to global xmc flusher.
            execution_id: Optional explicit execution ID.
        """
        super().__init__()
        self._name = name
        self._prefix = prefix
        self.client = client

        # Resolve active execution ID
        if execution_id:
            self._execution_id = execution_id
        elif client is not None and hasattr(client, "execution_id") and client.execution_id:
            self._execution_id = str(client.execution_id)
        else:
            self._execution_id = os.environ.get("XMC_EXECUTION_ID", "default_execution")

        self._version = version if version is not None else self._execution_id

    @property
    def name(self) -> str:
        """Returns the logger experiment name."""
        return self._name

    @property
    def version(self) -> str:
        """Returns the logger experiment version."""
        return self._version

    @property
    def execution_id(self) -> str:
        """Returns active XMC execution ID."""
        return self._execution_id

    @property
    def experiment(self) -> Any:
        """Returns underlying XMC client instance or self."""
        return self.client if self.client is not None else self

    @rank_zero_only
    def log_metrics(self, metrics: dict[str, Any], step: int | None = None) -> None:
        """Logs metrics dictionary to XMC SDK on rank zero."""
        if not metrics:
            return

        for key, val in metrics.items():
            scalar_val = extract_scalar(val)
            if scalar_val is not None:
                metric_name = f"{self._prefix}{key}" if self._prefix else key
                if self.client is not None:
                    self.client.log_metric(metric_name, scalar_val, step=step)
                else:
                    xmc.log_metric(metric_name, scalar_val, step=step)

    @rank_zero_only
    def log_hyperparams(self, params: dict[str, Any] | Any, *args: Any, **kwargs: Any) -> None:
        """Logs hyperparameters to XMC SDK on rank zero."""
        if not isinstance(params, dict):
            if hasattr(params, "__dict__"):
                params = vars(params)
            elif hasattr(params, "items"):
                params = dict(params.items())

        if isinstance(params, dict):
            for key, val in params.items():
                scalar_val = extract_scalar(val)
                if scalar_val is not None:
                    param_name = f"hp/{key}"
                    metric_name = f"{self._prefix}{param_name}" if self._prefix else param_name
                    if self.client is not None:
                        self.client.log_metric(metric_name, scalar_val, step=None)
                    else:
                        xmc.log_metric(metric_name, scalar_val, step=None)

    @rank_zero_only
    def save(self) -> None:
        """Flushes pending metrics on rank zero."""
        if self.client is not None and hasattr(self.client, "flush"):
            self.client.flush()
        elif hasattr(xmc, "flush"):
            xmc.flush()

    @rank_zero_only
    def finalize(self, status: str = "success") -> None:
        """Finalizes logging session and flushes metrics on rank zero."""
        self.save()
