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

RUN pip install --no-cache-dir grpcio-tools

COPY api/protos/metrics/metrics.proto /build/api/protos/metrics/metrics.proto
RUN python3 -m grpc_tools.protoc -I /build --python_out=/build \
    /build/api/protos/metrics/metrics.proto


FROM python:3.11-slim

WORKDIR /app

COPY --from=proto-builder /build/api /app/api

# The XMC SDK is the worker's only dependency. There is deliberately no database client
# here: the worker reaches the ingestion gateway over HTTP and nothing else.
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir protobuf /app/sdks/python/xmc

# metric_names.py ships alongside the worker because the launch script imports the same
# module to build the charts, which is what keeps the plotted names and the emitted
# names from drifting apart.
COPY examples/metrics_dashboard_job/metric_names.py /app/metric_names.py
COPY examples/metrics_dashboard_job/worker.py /app/worker.py
RUN chmod +x /app/worker.py

ENV PYTHONPATH=/app

# Build-time smoke check: verify that the XMC SDK and compiled metrics_pb2 proto bindings
# are importable and instantiable so broken proto packaging fails the image build early
# rather than crashing the worker pod at runtime.
RUN python3 -c "import xmc; from api.protos.metrics import metrics_pb2; metrics_pb2.Metric()"

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
