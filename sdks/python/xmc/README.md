# XMC Python SDK (`xmc`)

[![Build Status](https://img.shields.io/badge/build-passing-brightgreen)](#)
[![Python Version](https://img.shields.io/badge/python-3.11%2B-blue)](#)

The `xmc` Python SDK is a high-throughput, thread-safe metrics ingestion client designed for distributed Machine Learning (ML) workloads (PyTorch, JAX, TensorFlow) running across GKE, Vertex AI, Slurm, and local workstations.

---

## 📦 Installation

The SDK ships as the `xmc-metrics` distribution and is imported as `xmc`. Its only runtime dependency is `protobuf>=5.29.6,<7.0.0`.

> [!WARNING]
> Never run `pip install xmc`: that name on PyPI belongs to an unrelated package.

```bash
# From a google/xmc checkout (worker images, local development):
pip install ./sdks/python/xmc
pip install -e ./sdks/python/xmc   # editable

# Optional framework integrations:
pip install "./sdks/python/xmc[keras]"
pip install "./sdks/python/xmc[lightning]"
```

Building the package compiles [`api/protos/metrics/metrics.proto`](../../../api/protos/metrics/metrics.proto) into `xmc._protos.metrics_pb2` (see [`setup.py`](setup.py)), so a source install needs the repository's `api/protos/` next to `sdks/`. The sdist bundles that `.proto` and builds anywhere. The build pins `grpcio-tools==1.70.0` and `protobuf==5.29.6`: the generated module refuses to load on a protobuf runtime older than the one that generated it. `grpcio-tools==1.70.0` has no Python 3.14 wheel, so building from source on 3.14 compiles it first; the `xmc-metrics` wheel installs on 3.11–3.14 directly.

Editable installs write `_protos/metrics_pb2.py` into the source tree (git-ignored). Bazel builds don't generate it and import the Bazel-generated `api.protos.metrics.metrics_pb2` instead.

---

## 🏗️ Architecture & Thread Boundaries

To guarantee zero training loop stall, `xmc` separates the **Training Hot Path** from **Network Serialization & Flushing**.

```mermaid
flowchart TD
    subgraph HotPath["🔥 Training Hot Path (Main Thread)"]
        A["log_metric('loss', val, step)"] --> B["_coerce_float / _coerce_int<br/>(Detaches PyTorch/JAX .item())"]
        B --> C["is_valid_metric_name<br/>(LRU Cached Regex Check)"]
        C --> D["BoundedMetricsBuffer<br/>(Lightweight 4-element Tuple)"]
    end

    subgraph Flusher["⚡ Background Flusher Thread (Daemon)"]
        D -- Async Pop Batch --> E["harvest_client_metadata<br/>(XID, WU, Exec, Replica)"]
        E --> F["Instantiate Metric Protos<br/>(Float ts ➡️ Microsecond Int64)"]
        F --> G["format_metric_batch_payload<br/>(JSON Array + AIP-127 String IDs)"]
        G --> H["ResilienceStateMachine<br/>(Exponential Backoff & Retries)"]
        H --> I["HTTP Gateway / Transport"]
    end

    style HotPath fill:#ffebee,stroke:#c62828,stroke-width:2px
    style Flusher fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

---

## 🔄 Sequence Flow Diagram

```mermaid
sequenceDiagram
    autonumber
    actor Trainer as ML Trainer Thread
    participant Buffer as BoundedMetricsBuffer
    participant Worker as MetricsFlusher Daemon
    participant Gateway as XMC Ingestion Gateway

    Trainer->>Buffer: log_metric("loss", 0.123, step=10)
    Note over Trainer,Buffer: Non-blocking (~80B tuple insertion)
    Trainer->>Buffer: log_metric("accuracy", 0.95, step=10)

    loop Background Flush Interval (Default: 5s)
        Worker->>Buffer: pop_batch(batch_size=1000)
        Buffer-->>Worker: List of (name, val, step, timestamp_float)
        Worker->>Worker: Convert float ts to microsecond Int64
        Worker->>Worker: Harvest metadata (xid, work_unit_id, execution_id, replica_id)
        Worker->>Worker: Format XMCMetricsBatch JSON Payload
        Worker->>Gateway: HTTP POST /v1/metrics
        Gateway-->>Worker: 200 OK / Success
    end
```

---

## 🌐 Metadata Harvesting Hierarchy

`harvest_client_metadata()` automatically extracts cluster context for zero-boilerplate logging across orchestrated environments (GKE, Vertex AI, Slurm):

```mermaid
flowchart LR
    Tier1["1. Environment Vars<br/>(XMC_XID, XMC_WORK_UNIT_ID, ...)"] --> Tier2["2. Fail-Closed Early Exit<br/>(Silences telemetry)"]
```

1. **Environment Variables**: Primary source injected by XMC cluster infrastructure (`XMC_XID`, `XMC_WORK_UNIT_ID`, `XMC_EXECUTION_ID`, `XMC_REPLICA_ID`).
2. **Fail-Closed Missing Configs**: If `XMC_XID`, `XMC_WORK_UNIT_ID`, or `XMC_EXECUTION_ID` are missing, the SDK logs an error and exits the metric flow early (silences ingestion) instead of reverting to arbitrary fallback values. (`XMC_REPLICA_ID` naturally defaults to `"0"` for undiscovered ranks).

> [!NOTE]
> Per **Google AIP-127**, all 64-bit metadata IDs (`xid`, `work_unit_id`, `execution_id`, `replica_id`) are serialized as stringified integers (e.g. `"xid": "482019"`) in the JSON payload to prevent IEEE 754 float precision loss in web UIs.

---

## 🎯 Distributed ML Rank Detection & Silencing

In distributed training (`WORLD_SIZE > 1`), logging from every process can saturate network gateways. `xmc` auto-detects process rank:

- **Rank 0 (Chief / Driver)**: Actively logs metrics.
- **Rank > 0 (Workers)**: Automatically silenced unless `log_all_replicas=True`.

### Auto-Harvested Rank Variables:
- **PyTorch Distributed**: `RANK`, `WORLD_SIZE`
- **JAX Distributed**: `JAX_PROCESS_INDEX`
- **MPI**: `OMPI_COMM_WORLD_RANK`
- **Kubernetes / Vertex Jobs**: `JOB_COMPLETION_INDEX`, `XMC_JOB_INDEX`

---

## 💻 Quickstart Usage Examples

### 1. Basic Single-Process Logging

```python
import xmc

for step in range(1000):
    loss = train_step()
    xmc.log_metric("train/loss", loss, step=step)

# Optional: Ensure buffer is flushed before script exit
xmc.shutdown()
```

### 2. Distributed Training Setup (PyTorch / JAX)

```python
import xmc

for step in range(1000):
    loss = distributed_train_step()
    # Auto-detects process rank. Rank 0 logs to buffer; non-zero ranks drop locally.
    xmc.log_metric("train/loss", loss, step=step)

xmc.shutdown()
```

### 3. Opting Into All-Replica Logging

```python
import xmc

# Initialize the global flusher with log_all_replicas=True to capture metrics from every GPU rank
xmc.init(log_all_replicas=True)

xmc.log_metric("train/loss", loss, step=step)
xmc.shutdown()
```

---

## ⚡ Framework Integrations & Autologging

XMC provides two modes for framework integration: **Automatic Zero-Configuration Autologging (`xmc.autolog()`)** for zero-code-change telemetry, and **Explicit Callbacks & Loggers** for advanced customization.

---

### 1. Automatic Autologging (`xmc.autolog()`) [Recommended]

`xmc.autolog()` enables zero-configuration automatic framework patching for Keras (`keras>=3.0.0`) and PyTorch Lightning (`pytorch-lightning>=2.0.0`). When activated, `model.fit()` and `trainer.fit()` automatically log training/evaluation metrics without requiring manually instantiated callbacks or loggers.

```python
import xmc

# Activate automatic framework patching for Keras & PyTorch Lightning
xmc.autolog()

# Keras training automatically logs metrics without adding callbacks manually
model.fit(x_train, y_train, epochs=5)
```

#### ⚙️ Autologging Disable Knob & Override
- **Environment Variable**: Set `XMC_DISABLE_AUTOLOG=1` in your environment to safely disable autologging and skip framework patching without modifying code.
- **Explicit Disable Flag**: Call `xmc.autolog(disable=True)` to explicitly unpatch frameworks and disable autologging.

---

### 2. Explicit & Custom Integrations (Advanced Usage)

If you need custom metric prefixes, custom logging frequencies, or explicit `Client` instances, you can use the explicit integration classes directly instead of `xmc.autolog()`.

> [!TIP]
> **Combining `xmc.autolog()` with Explicit Callbacks**: If `xmc.autolog()` is active and an explicit `XmcKerasCallback` or `XmcLogger` is passed to `fit()`, XMC automatically detects the explicit instance and preserves it without adding a duplicate callback or logger.

#### 📦 Keras Explicit Callback (`XmcKerasCallback`)

```python
import keras
from xmc.integrations.keras import XmcKerasCallback

model = keras.Sequential([...])
model.compile(optimizer="adam", loss="mse", metrics=["accuracy"])

# Attach XmcKerasCallback manually with custom prefix or batch frequency
model.fit(
    x_train,
    y_train,
    epochs=5,
    callbacks=[XmcKerasCallback(prefix="train/", log_batch_frequency=10)],
)
```

#### 📦 PyTorch Lightning Explicit Logger (`XmcLogger`)

```python
import lightning.pytorch as pl
from xmc.integrations.lightning import XmcLogger

# Instantiate XmcLogger with custom experiment name and prefix
logger = XmcLogger(name="my_experiment", prefix="pl_metrics/")
trainer = pl.Trainer(logger=logger)
trainer.fit(model, dataloader)
```

#### 🔒 Guarantees & Operational Characteristics
- **Strict Decoupled Eager Imports**: Importing from `xmc.integrations.keras` or `xmc.integrations.lightning` validates version constraints eagerly at process startup (<10s fail-fast check). Missing or incompatible dependencies fail immediately before allocating GPU/TPU cluster resources.
- **Global Optimizer Step Mapping**: Batch and epoch metrics are mapped to the global optimizer iteration count (`model.optimizer.iterations`) to guarantee continuous time-series metrics across epoch boundaries in ClickHouse without step collisions.
- **Rank-Zero Silencing**: Automatically drops metric logging on worker ranks (`RANK > 0`) during multi-replica distributed training unless explicit multi-replica logging is configured.

> [!NOTE]
> **Legacy Keras 2 Support**: `XmcKerasCallback` requires `keras>=3.0.0`. For legacy Keras 2 (`tf.keras` <= 2.15), use `xmc.log_metric()` directly inside a `LambdaCallback`:
> ```python
> from tensorflow.keras.callbacks import LambdaCallback
> cb = LambdaCallback(on_epoch_end=lambda ep, logs: xmc.log_metric("loss", logs["loss"], step=ep))
> ```

---

## ⚙️ Resilience & Rate-Limited Drop Logging

- **Ring Buffer Overflow Protection**: `BoundedMetricsBuffer` defaults to 500,000 metrics (~85MB RAM). If network drops cause buffer overflow, oldest metrics are evicted FIFO.
- **Rate-Limited Warnings**: Drop warnings are logged at most once every **10 seconds** to prevent log stdout flooding.
- **Resilience State Machine**: Retries transient network/5xx errors using exponential backoff with jitter while immediately failing fast on terminal client errors (e.g., 400 Bad Request, 422 Unprocessable).

---

## 🧪 Running Tests & Linters

```bash
# Run all Bazel unit tests
bazel test //sdks/python/...

# Build the wheel and sdist (CI installs both via .github/workflows/xmc-metrics-package.yml
# and runs test/packaging/xmc_metrics_install_test.py against them)
python3 -m build sdks/python/xmc --outdir /tmp/xmc-metrics-dist

# Run pre-commit linters (Ruff, Buildifier, addlicense)
./.githooks/pre-commit
```
