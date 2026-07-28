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

"""Unit tests for XMC Keras integration subpackage."""

import importlib
import sys
import unittest
from unittest.mock import MagicMock, patch


class DummyCallback:
    """Base dummy callback class to stand in for keras.callbacks.Callback in tests."""

    def __init__(self) -> None:
        self.model = None


def _create_mock_keras(version: str = "3.1.0") -> MagicMock:
    """Helper to construct a mock keras module for testing."""
    mock_keras = MagicMock()
    mock_keras.__version__ = version
    mock_keras.callbacks.Callback = DummyCallback
    return mock_keras


class KerasIntegrationTest(unittest.TestCase):
    """Test suite for xmc.integrations.keras."""

    def setUp(self) -> None:
        """Clear sys.modules cache for xmc.integrations.keras before each test."""
        sys.modules.pop("xmc.integrations.keras", None)

    def test_import_fails_when_keras_missing(self) -> None:
        """Verifies eager import fails fast with ImportError when Keras is missing."""
        with patch.dict(sys.modules, {"keras": None}):
            with self.assertRaises(ImportError) as ctx:
                importlib.import_module("xmc.integrations.keras")
            self.assertIn("Keras is required to use XmcKerasCallback", str(ctx.exception))

    def test_import_fails_when_keras_version_incompatible(self) -> None:
        """Verifies eager import fails fast when Keras version < 3.0.0."""
        mock_keras = _create_mock_keras(version="2.15.0")
        with patch.dict(sys.modules, {"keras": mock_keras}):
            with self.assertRaises(ImportError) as ctx:
                importlib.import_module("xmc.integrations.keras")
            self.assertIn("Keras version >= 3.0.0 is required", str(ctx.exception))

    def test_import_succeeds_with_valid_keras(self) -> None:
        """Verifies eager import succeeds when Keras >= 3.0.0 is present."""
        mock_keras = _create_mock_keras(version="3.0.0")
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            self.assertTrue(hasattr(mod, "XmcKerasCallback"))

    def test_callback_logs_batch_metrics_with_optimizer_step(self) -> None:
        """Verifies on_train_batch_end maps metrics to optimizer iteration count."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            mock_client = MagicMock()
            callback = mod.XmcKerasCallback(prefix="train/", client=mock_client)

            mock_model = MagicMock()
            mock_model.optimizer.iterations.numpy.return_value = 150
            callback.model = mock_model

            logs = {"loss": 0.35, "accuracy": 0.88}
            callback.on_train_batch_end(batch=1, logs=logs)

            mock_client.log_metric.assert_any_call("train/loss", 0.35, step=150)
            mock_client.log_metric.assert_any_call("train/accuracy", 0.88, step=150)

    def test_callback_batch_frequency_throttling(self) -> None:
        """Verifies log_batch_frequency skips intermediate batches."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            mock_client = MagicMock()
            callback = mod.XmcKerasCallback(log_batch_frequency=2, client=mock_client)

            mock_model = MagicMock()
            mock_model.optimizer.iterations = 10
            callback.model = mock_model

            # Batch 1 (skipped)
            callback.on_train_batch_end(batch=1, logs={"loss": 0.5})
            mock_client.log_metric.assert_not_called()

            # Batch 2 (logged)
            callback.on_train_batch_end(batch=2, logs={"loss": 0.4})
            mock_client.log_metric.assert_called_once_with("loss", 0.4, step=10)

    def test_callback_logs_epoch_end_metrics(self) -> None:
        """Verifies on_epoch_end logs metrics correctly."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            mock_client = MagicMock()
            callback = mod.XmcKerasCallback(prefix="val/", client=mock_client)

            mock_model = MagicMock()
            mock_model.optimizer.iterations = 500
            callback.model = mock_model

            logs = {"val_loss": 0.25}
            callback.on_epoch_end(epoch=5, logs=logs)

            mock_client.log_metric.assert_called_once_with("val/val_loss", 0.25, step=500)

    def test_extract_scalar_types(self) -> None:
        """Verifies scalar extraction from various types (float, int, tensor item)."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")

            # Tensor item mock
            item_mock = MagicMock()
            item_mock.item.return_value = 1.23

            # Numpy mock
            np_mock = MagicMock()
            np_mock.numpy.return_value = 4.56

            self.assertEqual(mod.XmcKerasCallback._extract_scalar(42), 42)
            self.assertEqual(mod.XmcKerasCallback._extract_scalar(3.14), 3.14)
            self.assertEqual(mod.XmcKerasCallback._extract_scalar(item_mock), 1.23)
            self.assertEqual(mod.XmcKerasCallback._extract_scalar(np_mock), 4.56)
            self.assertIsNone(mod.XmcKerasCallback._extract_scalar("unsupported"))

    def test_global_xmc_flusher_fallback(self) -> None:
        """Verifies callback falls back to global xmc.log_metric when no client is passed."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            callback = mod.XmcKerasCallback()

            with patch("xmc.log_metric") as mock_global_log:
                callback.on_epoch_end(epoch=1, logs={"loss": 0.1})
                mock_global_log.assert_called_once_with("loss", 0.1, step=None)

    def test_pytorch_tensor_item_step_extraction(self) -> None:
        """Verifies _get_global_step extracts step from PyTorch/CUDA tensor item()."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            callback = mod.XmcKerasCallback()

            mock_model = MagicMock()
            iters_mock = MagicMock()
            iters_mock.item.return_value = 250
            del iters_mock.numpy
            mock_model.optimizer.iterations = iters_mock
            callback.model = mock_model

            self.assertEqual(callback._get_global_step(), 250)

    def test_on_epoch_begin_resets_batch_count(self) -> None:
        """Verifies on_epoch_begin resets batch counter to 0."""
        mock_keras = _create_mock_keras()
        with patch.dict(sys.modules, {"keras": mock_keras}):
            mod = importlib.import_module("xmc.integrations.keras")
            callback = mod.XmcKerasCallback(log_batch_frequency=2)
            callback._batch_count = 5

            callback.on_epoch_begin(epoch=1)
            self.assertEqual(callback._batch_count, 0)


if __name__ == "__main__":
    unittest.main()
