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

"""Unit tests for XMC Autologging entrypoint and framework patching."""

import os
import sys
import unittest
from typing import Any
from unittest.mock import MagicMock, patch

from xmc.autolog import (
    autolog,
    is_autolog_disabled,
    patch_keras,
    patch_lightning,
    unpatch_all,
)


class AutologTest(unittest.TestCase):
    """Test suite for xmc.autolog module."""

    def setUp(self) -> None:
        """Resets environment and unpatches frameworks before each test."""
        os.environ.pop("XMC_DISABLE_AUTOLOG", None)
        unpatch_all()

    def tearDown(self) -> None:
        """Cleans up environment and unpatches frameworks after each test."""
        os.environ.pop("XMC_DISABLE_AUTOLOG", None)
        unpatch_all()

    def test_is_autolog_disabled(self) -> None:
        """Verifies is_autolog_disabled returns True only when env var is '1'."""
        self.assertFalse(is_autolog_disabled())

        os.environ["XMC_DISABLE_AUTOLOG"] = "1"
        self.assertTrue(is_autolog_disabled())

        os.environ["XMC_DISABLE_AUTOLOG"] = "0"
        self.assertFalse(is_autolog_disabled())

        os.environ["XMC_DISABLE_AUTOLOG"] = "true"
        self.assertFalse(is_autolog_disabled())

    def test_autolog_activation_emits_log(self) -> None:
        """Verifies autolog() emits 'INFO: xmc: Autologging activated...' and returns True."""
        with (
            patch("xmc.autolog.logger.info") as mock_log_info,
            patch("xmc.autolog.patch_keras", return_value=True),
        ):
            result = autolog()
            self.assertTrue(result)
            mock_log_info.assert_called_with("INFO: xmc: Autologging activated...")

    def test_autolog_disabled_via_env_var(self) -> None:
        """Verifies autolog() returns False and logs disable message when XMC_DISABLE_AUTOLOG=1."""
        os.environ["XMC_DISABLE_AUTOLOG"] = "1"
        with patch("xmc.autolog.logger.info") as mock_log_info:
            result = autolog()
            self.assertFalse(result)
            mock_log_info.assert_called_with(
                "INFO: xmc: Autologging disabled via XMC_DISABLE_AUTOLOG=1 or disable flag."
            )

    def test_autolog_disabled_via_disable_flag(self) -> None:
        """Verifies autolog(disable=True) returns False and disables autologging."""
        with patch("xmc.autolog.logger.info") as mock_log_info:
            result = autolog(disable=True)
            self.assertFalse(result)
            mock_log_info.assert_called_with(
                "INFO: xmc: Autologging disabled via XMC_DISABLE_AUTOLOG=1 or disable flag."
            )

    def test_patch_keras_injects_callback_kwarg(self) -> None:
        """Verifies patch_keras wraps Model.fit and injects XmcKerasCallback via kwargs."""
        mock_fit = MagicMock(return_value="fit_result")

        def dummy_fit(self, x=None, y=None, callbacks=None):
            return mock_fit(self, x=x, y=y, callbacks=callbacks)

        mock_model_cls = type("Model", (), {"fit": dummy_fit})
        mock_keras = MagicMock()
        mock_keras.Model = mock_model_cls
        mock_callback_cls = type("XmcKerasCallback", (), {})

        with patch.dict(
            sys.modules,
            {
                "keras": mock_keras,
                "xmc.integrations.keras": MagicMock(XmcKerasCallback=mock_callback_cls),
            },
        ):
            success = patch_keras()
            self.assertTrue(success)

            model_inst = mock_model_cls()
            res = model_inst.fit("x_data", "y_data")
            self.assertEqual(res, "fit_result")

            mock_fit.assert_called_once()
            _, kwargs = mock_fit.call_args
            self.assertIn("callbacks", kwargs)
            callbacks = kwargs["callbacks"]
            self.assertEqual(len(callbacks), 1)
            self.assertIsInstance(callbacks[0], mock_callback_cls)

    def test_patch_keras_positional_callbacks_argument(self) -> None:
        """Verifies patch_keras handles callbacks passed positionally without TypeError."""
        mock_fit = MagicMock(return_value="fit_result")

        def dummy_fit(self, x=None, y=None, batch_size=None, callbacks=None):
            return mock_fit(self, x, y, batch_size, callbacks)

        mock_model_cls = type("Model", (), {"fit": dummy_fit})
        mock_keras = MagicMock()
        mock_keras.Model = mock_model_cls
        mock_callback_cls = type("XmcKerasCallback", (), {})

        with patch.dict(
            sys.modules,
            {
                "keras": mock_keras,
                "xmc.integrations.keras": MagicMock(XmcKerasCallback=mock_callback_cls),
            },
        ):
            success = patch_keras()
            self.assertTrue(success)

            model_inst = mock_model_cls()
            custom_cbs: list[Any] = []
            # Pass callbacks positionally as 4th argument
            res = model_inst.fit("x_data", "y_data", 32, custom_cbs)
            self.assertEqual(res, "fit_result")

            mock_fit.assert_called_once()
            args = mock_fit.call_args[0]
            # Verify callbacks in positional args contains injected XmcKerasCallback
            passed_callbacks = args[4]
            self.assertEqual(len(passed_callbacks), 1)
            self.assertIsInstance(passed_callbacks[0], mock_callback_cls)

    def test_patch_lightning_injects_logger(self) -> None:
        """Verifies patch_lightning wraps Trainer.fit and injects XmcLogger safely."""
        mock_fit = MagicMock(return_value="trainer_fit_result")

        class DummyTrainer:
            def __init__(self) -> None:
                self._loggers: list[object] | None = None

            @property
            def loggers(self) -> list[object]:
                return self._loggers if self._loggers is not None else []

            @property
            def logger(self) -> object:
                return self.loggers[0] if self.loggers else None

            def fit(self, model: object, dataloader: object) -> object:
                return mock_fit(self, model, dataloader)

        mock_pl = MagicMock()
        mock_pl.Trainer = DummyTrainer
        mock_lightning = MagicMock()
        mock_lightning.pytorch = mock_pl
        mock_logger_cls = type("XmcLogger", (), {})

        with patch.dict(
            sys.modules,
            {
                "lightning": mock_lightning,
                "lightning.pytorch": mock_pl,
                "xmc.integrations.lightning": MagicMock(XmcLogger=mock_logger_cls),
            },
        ):
            success = patch_lightning()
            self.assertTrue(success)

            trainer_inst = DummyTrainer()
            res = trainer_inst.fit("model", "dataloader")
            self.assertEqual(res, "trainer_fit_result")

            mock_fit.assert_called_once()
            self.assertEqual(len(trainer_inst.loggers), 1)
            self.assertIsInstance(trainer_inst.loggers[0], mock_logger_cls)

    def test_patch_keras_preserves_existing_callback(self) -> None:
        """Verifies patch_keras does not duplicate XmcKerasCallback if already present."""
        mock_fit = MagicMock(return_value="fit_result")

        def dummy_fit(self: Any, x: Any = None, y: Any = None, callbacks: Any = None) -> Any:
            return mock_fit(self, x=x, y=y, callbacks=callbacks)

        mock_model_cls = type("Model", (), {"fit": dummy_fit})
        mock_keras = MagicMock()
        mock_keras.Model = mock_model_cls
        mock_callback_cls = type("XmcKerasCallback", (), {})

        with patch.dict(
            sys.modules,
            {
                "keras": mock_keras,
                "xmc.integrations.keras": MagicMock(XmcKerasCallback=mock_callback_cls),
            },
        ):
            success = patch_keras()
            self.assertTrue(success)

            model_inst = mock_model_cls()
            existing_cb = mock_callback_cls()
            res = model_inst.fit("x_data", "y_data", callbacks=[existing_cb])
            self.assertEqual(res, "fit_result")

            mock_fit.assert_called_once()
            _, kwargs = mock_fit.call_args
            callbacks = kwargs["callbacks"]
            # Verify no duplicate callback was added
            self.assertEqual(len(callbacks), 1)
            self.assertIs(callbacks[0], existing_cb)

    def test_patch_lightning_preserves_existing_logger(self) -> None:
        """Verifies patch_lightning does not duplicate XmcLogger if already present."""
        mock_fit = MagicMock(return_value="trainer_fit_result")

        class DummyTrainer:
            def __init__(self) -> None:
                self._loggers: list[object] | None = None

            @property
            def loggers(self) -> list[object]:
                return self._loggers if self._loggers is not None else []

            @property
            def logger(self) -> object:
                return self.loggers[0] if self.loggers else None

            def fit(self, model: object, dataloader: object) -> object:
                return mock_fit(self, model, dataloader)

        mock_pl = MagicMock()
        mock_pl.Trainer = DummyTrainer
        mock_lightning = MagicMock()
        mock_lightning.pytorch = mock_pl
        mock_logger_cls = type("XmcLogger", (), {})

        with patch.dict(
            sys.modules,
            {
                "lightning": mock_lightning,
                "lightning.pytorch": mock_pl,
                "xmc.integrations.lightning": MagicMock(XmcLogger=mock_logger_cls),
            },
        ):
            success = patch_lightning()
            self.assertTrue(success)

            trainer_inst = DummyTrainer()
            existing_logger = mock_logger_cls()
            trainer_inst._loggers = [existing_logger]
            res = trainer_inst.fit("model", "dataloader")
            self.assertEqual(res, "trainer_fit_result")

            mock_fit.assert_called_once()
            # Verify no duplicate logger was added
            self.assertEqual(len(trainer_inst.loggers), 1)
            self.assertIs(trainer_inst.loggers[0], existing_logger)


if __name__ == "__main__":
    unittest.main()
