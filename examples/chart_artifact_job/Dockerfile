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

FROM python:3.11-slim

WORKDIR /app

# Copy proto definitions and install the XMC Python SDK used to log metrics.
COPY api/protos/metrics /app/api/protos/metrics
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir protobuf /app/sdks/python/xmc

# `training_metrics.py` is shared with launch.py so the metric names cannot drift.
COPY examples/chart_artifact_job/training_metrics.py /app/training_metrics.py
COPY examples/chart_artifact_job/worker.py /app/worker.py

RUN chmod +x /app/worker.py

ENV PYTHONPATH=/app

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
