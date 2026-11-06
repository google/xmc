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

# DEV/TEST ONLY Open Source Vizier server. Build from the repository root:
#   docker build -t xmc-vizier:0.1.24 test/e2e/testdata/vizier
# Base image: python:3.11.18-slim, pinned by digest.
FROM python:3.11.18-slim@sha256:db20d8506e2f15b6db39b40b6015ca46bae855dec6dd326d462cfe79ae972893

# HOME=/tmp: the only writable path besides /data under the read-only root filesystem.
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    HOME=/tmp

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY server_env.py supported_algorithms.py vizier_server.py ./

RUN groupadd --system --gid 10001 vizier \
    && useradd --system --uid 10001 --gid vizier --no-create-home vizier \
    && mkdir -p /data \
    && chown vizier:vizier /data
USER 10001:10001

EXPOSE 7077
ENTRYPOINT ["python", "/app/vizier_server.py"]
