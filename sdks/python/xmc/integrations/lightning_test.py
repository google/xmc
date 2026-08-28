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

"""Unit tests for XMC PyTorch Lightning integration subpackage."""

import importlib
import sys
import unittest
from unittest.mock import MagicMock, patch


class DummyLogger:
    """Base dummy logger class to stand in for Logger in tests."""

    def __init__(self) -> None:
        pass


def _identity_decorator(fn: type) -> type:
    return fn


def _create_mock_lightning(version: str = "2.1.0") -> tuple[MagicMock, MagicMock]:
    """Helper to construct mock lightning and pytorch_lightning modules for testing."""
    mock_pl = MagicMock()
    mock_pl.__version__ = version
    mock_pl.loggers.Logger = DummyLogger
    mock_pl.utilities.rank_zero_only = _identity_decorator
    return mock_pl, mock_pl


class LightningIntegrationTest(unittest.TestCase):
    """Test suite for xmc.integrations.lightning."""

    def setUp(self) -> None:
        """Clear sys.modules cache for xmc.integrations.lightning before each test."""
        sys.modules.pop("xmc.integrations.lightning", None)

    def test_import_fails_when_lightning_missing(self) -> None:
        """Verifies eager import fails fast with ImportError when PyTorch Lightning is missing."""
        with patch.dict(sys.modules, {"lightning.pytorch": None, "pytorch_lightning": None}):
            with self.assertRaises(ImportError) as ctx:
                importlib.import_module("xmc.integrations.lightning")
            self.assertIn("PyTorch Lightning is required to use XmcLogger", str(ctx.exception))

    def test_import_fails_when_lightning_version_incompatible(self) -> None:
        """Verifies eager import fails fast when PyTorch Lightning version < 2.0.0."""
        mock_pl, _ = _create_mock_lightning(version="1.9.0")
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            with self.assertRaises(ImportError) as ctx:
                importlib.import_module("xmc.integrations.lightning")
            self.assertIn("PyTorch Lightning version >= 2.0.0 is required", str(ctx.exception))

    def test_import_succeeds_with_valid_lightning(self) -> None:
        """Verifies eager import succeeds when PyTorch Lightning >= 2.0.0 is present."""
        for version in ("2.0.0", "2.0", "2"):
            with self.subTest(version=version):
                sys.modules.pop("xmc.integrations.lightning", None)
                mock_pl, _ = _create_mock_lightning(version=version)
                with patch.dict(
                    sys.modules,
                    {
                        "lightning.pytorch": mock_pl,
                        "lightning.pytorch.loggers": mock_pl.loggers,
                        "lightning.pytorch.utilities": mock_pl.utilities,
                        "pytorch_lightning": mock_pl,
                        "pytorch_lightning.loggers": mock_pl.loggers,
                        "pytorch_lightning.utilities": mock_pl.utilities,
                    },
                ):
                    mod = importlib.import_module("xmc.integrations.lightning")
                    self.assertTrue(hasattr(mod, "XmcLogger"))

    def test_logger_properties(self) -> None:
        """Verifies name, version, execution_id, and experiment properties."""
        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")
            mock_client = MagicMock()
            mock_client.execution_id = "exec_12345"

            logger = mod.XmcLogger(
                name="test_experiment",
                version="v1.0",
                client=mock_client,
            )

            self.assertEqual(logger.name, "test_experiment")
            self.assertEqual(logger.version, "v1.0")
            self.assertEqual(logger.execution_id, "exec_12345")
            self.assertEqual(logger.experiment, mock_client)

    def test_logger_log_metrics(self) -> None:
        """Verifies log_metrics passes metric values to client with prefix and step."""
        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")
            mock_client = MagicMock()
            logger = mod.XmcLogger(prefix="train/", client=mock_client)

            metrics = {"loss": 0.42, "accuracy": 0.95}
            logger.log_metrics(metrics, step=100)

            mock_client.log_metric.assert_any_call("train/loss", 0.42, step=100)
            mock_client.log_metric.assert_any_call("train/accuracy", 0.95, step=100)

    def test_logger_log_hyperparams(self) -> None:
        """Verifies log_hyperparams logs parameters with hp/ prefix from dict and Namespace."""
        from argparse import Namespace

        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")
            mock_client = MagicMock()
            logger = mod.XmcLogger(prefix="exp1/", client=mock_client)

            # Test with dict
            params_dict = {"learning_rate": 0.001, "batch_size": 32}
            logger.log_hyperparams(params_dict)
            mock_client.log_metric.assert_any_call("exp1/hp/learning_rate", 0.001, step=None)
            mock_client.log_metric.assert_any_call("exp1/hp/batch_size", 32, step=None)

            mock_client.log_metric.reset_mock()

            # Test with Namespace
            params_ns = Namespace(learning_rate=0.001, batch_size=32)
            logger.log_hyperparams(params_ns)
            mock_client.log_metric.assert_any_call("exp1/hp/learning_rate", 0.001, step=None)
            mock_client.log_metric.assert_any_call("exp1/hp/batch_size", 32, step=None)

            mock_client.log_metric.assert_any_call("exp1/hp/learning_rate", 0.001, step=None)
            mock_client.log_metric.assert_any_call("exp1/hp/batch_size", 32, step=None)

    def test_logger_save_and_finalize(self) -> None:
        """Verifies save and finalize invoke client flush."""
        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")
            mock_client = MagicMock()
            logger = mod.XmcLogger(client=mock_client)

            logger.save()
            mock_client.flush.assert_called_once()

            mock_client.flush.reset_mock()
            logger.finalize(status="success")
            mock_client.flush.assert_called_once()

    def test_extract_scalar_types(self) -> None:
        """Verifies scalar extraction helper with various object types."""
        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")

            item_mock = MagicMock()
            item_mock.item.return_value = 2.718

            np_mock = MagicMock()
            np_mock.numpy.return_value = 3.14

            self.assertEqual(mod.extract_scalar(10), 10)
            self.assertEqual(mod.extract_scalar(0.5), 0.5)
            self.assertEqual(mod.extract_scalar(item_mock), 2.718)
            self.assertEqual(mod.extract_scalar(np_mock), 3.14)
            self.assertIsNone(mod.extract_scalar("string"))

    def test_global_xmc_flusher_fallback(self) -> None:
        """Verifies logger falls back to global xmc.log_metric when no client is provided."""
        mock_pl, _ = _create_mock_lightning()
        with patch.dict(
            sys.modules,
            {
                "lightning.pytorch": mock_pl,
                "lightning.pytorch.loggers": mock_pl.loggers,
                "lightning.pytorch.utilities": mock_pl.utilities,
                "pytorch_lightning": mock_pl,
                "pytorch_lightning.loggers": mock_pl.loggers,
                "pytorch_lightning.utilities": mock_pl.utilities,
            },
        ):
            mod = importlib.import_module("xmc.integrations.lightning")
            logger = mod.XmcLogger()

            with patch("xmc.log_metric") as mock_global_log:
                logger.log_metrics({"val_loss": 0.15}, step=5)
                mock_global_log.assert_called_once_with("val_loss", 0.15, step=5)


if __name__ == "__main__":
    unittest.main()
