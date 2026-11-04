#!/usr/bin/env python3
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

"""A training worker script demonstrating XMC PyTorch Lightning autologging integration."""

import os

import pytorch_lightning as pl
import torch
import xmc
from torch.utils.data import DataLoader, TensorDataset


class HelloLightningModel(pl.LightningModule):  # type: ignore[misc]  # pytorch_lightning is untyped
    """Simple PyTorch Lightning model for autologging demonstration."""

    def __init__(self) -> None:
        super().__init__()
        self.layer = torch.nn.Linear(10, 1)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.layer(x)

    def training_step(
        self, batch: tuple[torch.Tensor, torch.Tensor], batch_idx: int
    ) -> torch.Tensor:
        x, y = batch
        y_hat = self(x)
        loss = torch.nn.functional.mse_loss(y_hat, y)
        acc = torch.tensor(0.5 + (0.04 * batch_idx), dtype=torch.float32)

        self.log("pl/loss", loss, on_step=True, on_epoch=True)
        self.log("pl/accuracy", acc, on_step=True, on_epoch=True)
        return loss

    def configure_optimizers(self) -> torch.optim.Optimizer:
        return torch.optim.SGD(self.parameters(), lr=0.01)


def main() -> None:
    """Entry point for the PyTorch Lightning autologging metrics worker process."""
    print("=== XMC PyTorch Lightning Autologging Example Worker Started ===")
    print(f"XMC_GATEWAY_URL: {os.environ.get('XMC_GATEWAY_URL')}")
    print(f"XMC_XID: {os.environ.get('XMC_XID')}")
    print(f"XMC_WORK_UNIT_ID: {os.environ.get('XMC_WORK_UNIT_ID')}")
    print(f"XMC_EXECUTION_ID: {os.environ.get('XMC_EXECUTION_ID')}")

    # Activate autologging for PyTorch Lightning
    autolog_active = xmc.autolog()
    print(f"Autologging status: {autolog_active}")

    # Create synthetic dataset (100 samples, batch_size=10 -> 10 steps per epoch)
    print("Creating synthetic PyTorch dataset...")
    x_data = torch.randn(100, 10, dtype=torch.float32)
    y_data = torch.randn(100, 1, dtype=torch.float32)
    dataset = TensorDataset(x_data, y_data)
    dataloader = DataLoader(dataset, batch_size=10)

    model = HelloLightningModel()
    trainer = pl.Trainer(
        default_root_dir="/tmp",
        max_epochs=2,
        log_every_n_steps=1,
        enable_checkpointing=False,
        enable_progress_bar=False,
    )

    try:
        print("Starting PyTorch Lightning trainer.fit()...")
        # xmc.autolog() automatically attaches XmcLogger to trainer.fit()
        trainer.fit(model, dataloader)
    finally:
        print("Flushing remaining buffered metrics...")
        xmc.shutdown()
        print("=== XMC PyTorch Lightning Autologging Worker Completed ===")


if __name__ == "__main__":
    main()
