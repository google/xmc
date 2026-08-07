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

"""A training worker script demonstrating XMC Keras autologging integration."""

import os

# Ensure Keras uses torch backend if tf/jax are not present
os.environ.setdefault("KERAS_BACKEND", "torch")

import keras
import numpy as np
import xmc


def main() -> None:
    """Entry point for the Keras autologging metrics worker process."""
    print("=== XMC Keras Autologging Example Worker Started ===")
    print(f"XMC_GATEWAY_URL: {os.environ.get('XMC_GATEWAY_URL')}")
    print(f"XMC_XID: {os.environ.get('XMC_XID')}")
    print(f"XMC_WORK_UNIT_ID: {os.environ.get('XMC_WORK_UNIT_ID')}")
    print(f"XMC_EXECUTION_ID: {os.environ.get('XMC_EXECUTION_ID')}")

    # Activate autologging for Keras
    autolog_active = xmc.autolog()
    print(f"Autologging status: {autolog_active}")

    # Create synthetic dataset
    print("Creating synthetic training dataset...")
    x_train = np.random.random((100, 10)).astype(np.float32)
    y_train = np.random.random((100, 1)).astype(np.float32)

    # Build simple Keras model
    model = keras.Sequential(
        [
            keras.layers.Dense(10, activation="relu"),
            keras.layers.Dense(1),
        ]
    )
    model.compile(optimizer="sgd", loss="mse", metrics=["accuracy"])

    try:
        print("Starting Keras model.fit() for 5 epochs...")
        # xmc.autolog() automatically attaches XmcKerasCallback to model.fit()
        model.fit(
            x_train,
            y_train,
            epochs=5,
            batch_size=10,
            verbose=1,
        )
    finally:
        print("Flushing remaining buffered metrics...")
        xmc.shutdown()
        print("=== XMC Keras Autologging Worker Completed ===")


if __name__ == "__main__":
    main()
