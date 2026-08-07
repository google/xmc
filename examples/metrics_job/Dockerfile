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

# Copy proto definitions and compiled python proto package
COPY api/protos/metrics /app/api/protos/metrics

# Copy and install XMC Python SDK, protobuf, PyTorch (CPU), PyTorch Lightning, and Keras 3
COPY sdks/python/xmc /app/sdks/python/xmc
RUN pip install --no-cache-dir \
    protobuf \
    torch --extra-index-url https://download.pytorch.org/whl/cpu \
    pytorch-lightning \
    "keras>=3.0.0" \
    /app/sdks/python/xmc

# Copy worker scripts for manual SDK, Keras autologging, and PyTorch Lightning autologging
COPY examples/metrics_job/worker.py /app/worker.py
COPY examples/metrics_job/worker_keras.py /app/worker_keras.py
COPY examples/metrics_job/worker_lightning.py /app/worker_lightning.py

RUN chmod +x /app/worker.py /app/worker_keras.py /app/worker_lightning.py

ENV PYTHONPATH=/app

ENTRYPOINT ["python3", "-u", "/app/worker.py"]
