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

"""Endpoint resolution helpers for XMC nightly verification scripts."""

from __future__ import annotations

import os

from tools.nightly_tests import xmc_entities_validator


def resolve_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ESS gRPC endpoint from parameter, environment, or default."""
    if ep := str(provided_endpoint or "").strip():
        return ep
    return os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")


def resolve_dashboard_endpoint(provided_endpoint: str | None = None) -> str:
    """Derives HTTP REST base endpoint for dashboard validation."""
    if not (ep := str(provided_endpoint or "").strip()):
        return resolve_ui_base_url()
    if ep.startswith(("http://", "https://")):
        return ep
    return f"http://{ep}" if ep.startswith(("localhost", "127.0.0.1")) else f"https://{ep}"


def resolve_clickhouse_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ClickHouse HTTP endpoint from parameter, environment, or default."""
    if not (
        ep := str(provided_endpoint or "").strip()
        or os.environ.get("CLICKHOUSE_ENDPOINT", "").strip()
    ):
        if host := os.environ.get("CLICKHOUSE_HOST", "").strip():
            port = os.environ.get(
                "CLICKHOUSE_HTTP_PORT", os.environ.get("CLICKHOUSE_PORT", "8123")
            ).strip()
            ep = f"{host}:{port}"
        else:
            ep = "http://127.0.0.1:8123"

    if not ep.startswith(("http://", "https://")):
        ep = f"http://{ep}"
    return ep.rstrip("/")


def resolve_ui_base_url() -> str:
    """Resolves Web UI base URL via xmc_entities_validator."""
    return xmc_entities_validator.resolve_ui_base_url()
