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

FROM python:3.11-slim

WORKDIR /app

# The XMC SDK is the worker's only dependency. There is deliberately no database client
# here: the worker reaches the ingestion gateway over HTTP and nothing else.
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir /app/sdks/python/xmc

# metric_names.py ships alongside the worker because the launch script imports the same
# module to build the charts, which is what keeps the plotted names and the emitted
# names from drifting apart.
COPY examples/metrics_dashboard_job/metric_names.py /app/metric_names.py
COPY examples/metrics_dashboard_job/worker.py /app/worker.py
RUN chmod +x /app/worker.py

ENV PYTHONPATH=/app

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
