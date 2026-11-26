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

FROM python:3.11-slim AS proto-builder

WORKDIR /build

# Keep in sync with [build-system] in sdks/python/xmc/pyproject.toml. xmc-metrics
# caps protobuf below 7, and generated code refuses older runtimes, so this copy
# must be generated with the same protobuf 5.29 floor.
RUN pip install --no-cache-dir grpcio-tools==1.70.0

COPY api/protos/metrics/metrics.proto /build/api/protos/metrics/metrics.proto
RUN python3 -m grpc_tools.protoc -I /build --python_out=/build \
    /build/api/protos/metrics/metrics.proto


FROM python:3.11-slim

WORKDIR /app

# Copy compiled python proto package from builder stage. It also carries
# api/protos/metrics/metrics.proto, which installing sdks/python/xmc (xmc-metrics)
# compiles into xmc._protos; keep the .proto in this copy.
COPY --from=proto-builder /build/api /app/api

# Copy and install XMC Python SDK, protobuf, PyTorch (CPU), PyTorch Lightning, and Keras 3
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir \
    protobuf \
    torch --extra-index-url https://download.pytorch.org/whl/cpu \
    pytorch-lightning \
    "keras>=3.0.0" \
    /app/sdks/python/xmc

# Copy worker scripts for manual SDK, Keras autologging, Keras callback, and PyTorch Lightning autologging
COPY examples/metrics_job/worker.py /app/worker.py
COPY examples/metrics_job/worker_keras.py /app/worker_keras.py
COPY examples/metrics_job/worker_keras_callback.py /app/worker_keras_callback.py
COPY examples/metrics_job/worker_lightning.py /app/worker_lightning.py

RUN chmod +x /app/worker.py /app/worker_keras.py /app/worker_keras_callback.py /app/worker_lightning.py

ENV PYTHONPATH=/app

# Build-time smoke check: verify that the XMC SDK and compiled metrics_pb2 proto bindings
# are importable and instantiable so broken proto packaging fails the image build early
# rather than crashing the worker pod at runtime.
RUN python3 -c "import xmc; from api.protos.metrics import metrics_pb2; metrics_pb2.Metric()"

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
