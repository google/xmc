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

"""XMC Autologging Entrypoint and Framework Patching Module."""

import functools
import inspect
import logging
import os
from collections.abc import Callable
from typing import Any

logger = logging.getLogger(__name__)

_AUTOLOG_DISABLED_ENV = "XMC_DISABLE_AUTOLOG"
_patched_keras_original_fit: Callable[..., Any] | None = None
_patched_lightning_original_fit: Callable[..., Any] | None = None


def is_autolog_disabled() -> bool:
    """Checks if autologging is disabled via XMC_DISABLE_AUTOLOG environment variable."""
    return os.environ.get(_AUTOLOG_DISABLED_ENV) == "1"


def patch_keras() -> bool:
    """Safely patches Keras Model.fit to automatically attach XmcKerasCallback.

    Returns:
        True if Keras was successfully patched, False otherwise.
    """
    global _patched_keras_original_fit
    if _patched_keras_original_fit is not None:
        return True

    try:
        import keras

        from xmc.integrations.keras import XmcKerasCallback

        original_fit = keras.Model.fit
        sig = inspect.signature(original_fit)

        @functools.wraps(original_fit)
        def custom_fit(self: Any, *args: Any, **kwargs: Any) -> Any:
            try:
                bound_args = sig.bind(self, *args, **kwargs)
                bound_args.apply_defaults()
                callbacks = bound_args.arguments.get("callbacks", [])
                callbacks_list = list(callbacks) if callbacks else []
                if not any(isinstance(c, XmcKerasCallback) for c in callbacks_list):
                    callbacks_list.append(XmcKerasCallback())
                    bound_args.arguments["callbacks"] = callbacks_list
                return original_fit(*bound_args.args, **bound_args.kwargs)
            except TypeError:
                return original_fit(self, *args, **kwargs)

        keras.Model.fit = custom_fit
        _patched_keras_original_fit = original_fit
        return True
    except (ImportError, Exception):
        return False


def unpatch_keras() -> None:
    """Restores original Keras Model.fit method if patched."""
    global _patched_keras_original_fit
    if _patched_keras_original_fit is not None:
        try:
            import keras

            keras.Model.fit = _patched_keras_original_fit
        except Exception:
            pass
        _patched_keras_original_fit = None


def patch_lightning() -> bool:
    """Safely patches PyTorch Lightning Trainer.fit to automatically attach XmcLogger.

    Returns:
        True if PyTorch Lightning was successfully patched, False otherwise.
    """
    global _patched_lightning_original_fit
    if _patched_lightning_original_fit is not None:
        return True

    try:
        try:
            import lightning.pytorch as pl
        except ImportError:
            import pytorch_lightning as pl

        from xmc.integrations.lightning import XmcLogger

        original_fit = pl.Trainer.fit

        @functools.wraps(original_fit)
        def custom_fit(self: Any, *args: Any, **kwargs: Any) -> Any:
            loggers = getattr(self, "loggers", None)
            if loggers is None and hasattr(self, "_loggers"):
                loggers = self._loggers

            if isinstance(loggers, list):
                if not any(isinstance(logger_inst, XmcLogger) for logger_inst in loggers):
                    loggers.append(XmcLogger())
            return original_fit(self, *args, **kwargs)

        pl.Trainer.fit = custom_fit
        _patched_lightning_original_fit = original_fit
        return True
    except (ImportError, Exception):
        return False


def unpatch_lightning() -> None:
    """Restores original PyTorch Lightning Trainer.fit method if patched."""
    global _patched_lightning_original_fit
    if _patched_lightning_original_fit is not None:
        try:
            try:
                import lightning.pytorch as pl
            except ImportError:
                import pytorch_lightning as pl

            pl.Trainer.fit = _patched_lightning_original_fit
        except Exception:
            pass
        _patched_lightning_original_fit = None


def unpatch_all() -> None:
    """Restores all patched framework methods."""
    unpatch_keras()
    unpatch_lightning()


def autolog(disable: bool = False) -> bool:
    """Activates automatic framework autologging for Keras and PyTorch Lightning.

    Args:
        disable: If True, explicitly disables autologging and unpatches frameworks.

    Returns:
        True if autologging is active and patched, False if disabled.
    """
    if disable or is_autolog_disabled():
        logger.info("INFO: xmc: Autologging disabled via XMC_DISABLE_AUTOLOG=1 or disable flag.")
        unpatch_all()
        return False

    keras_patched = patch_keras()
    lightning_patched = patch_lightning()

    logger.info("INFO: xmc: Autologging activated...")
    return keras_patched or lightning_patched
