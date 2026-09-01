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

# Build from the repository root so the XMC SDK sources are inside the build context:
#   docker build -f examples/chart_artifact_job/Dockerfile <repo-root>

# `xmc.types` does `from api.protos.metrics import metrics_pb2`, and that module is
# generated rather than checked in -- in-tree it comes from the Bazel py_proto_library in
# api/protos/metrics/BUILD.bazel. There is no Bazel in the image, so compile the proto
# with protoc in a builder stage and copy only the generated module into the runtime
# stage, keeping grpcio-tools out of the final image.
FROM python:3.11-slim AS proto-builder

WORKDIR /build

RUN pip install --no-cache-dir grpcio-tools

# metrics.proto imports nothing, so the repository root is the only proto path needed.
COPY api/protos/metrics/metrics.proto /build/api/protos/metrics/metrics.proto
RUN python3 -m grpc_tools.protoc -I /build --python_out=/build \
    /build/api/protos/metrics/metrics.proto


FROM python:3.11-slim

WORKDIR /app

# api/, api/protos/ and api/protos/metrics/ carry no __init__.py; they resolve as PEP 420
# namespace packages off PYTHONPATH below.
COPY --from=proto-builder /build/api /app/api

# Install the XMC Python SDK used to log metrics.
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir protobuf /app/sdks/python/xmc

# `training_metrics.py` is shared with launch.py so the metric names cannot drift.
COPY examples/chart_artifact_job/training_metrics.py /app/training_metrics.py
COPY examples/chart_artifact_job/worker.py /app/worker.py

RUN chmod +x /app/worker.py

ENV PYTHONPATH=/app

# Fail the build rather than the pod if the generated proto is not importable.
RUN python3 -c "import xmc; from api.protos.metrics import metrics_pb2; metrics_pb2.Metric()"

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
