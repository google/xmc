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

"""Launcher script to call XMC Dashboard Service and list available dashboards."""

import logging
import os
from collections.abc import Sequence

import dashboard
import dotenv
import grpc
from absl import app


def main(argv: Sequence[str]) -> None:
    """Attempts to call Dashboard Service and list available dashboards."""
    logging.basicConfig(level=logging.INFO)
    logger = logging.getLogger("dashboard_launcher")
    logging.getLogger("dotenv.main").setLevel(logging.ERROR)
    dotenv.load_dotenv()

    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    endpoint = os.environ.get("XMANAGER_DASHBOARD_ENDPOINT", "localhost:8080")
    logger.info("Connecting to XMC Dashboard Service at endpoint: %s", endpoint)

    try:
        dashboards = dashboard.list_dashboards()
        logger.info("Successfully retrieved %d dashboard(s).", len(dashboards))
        for d in dashboards:
            logger.info(
                "Dashboard - Name: %s | ID: %s | Title: %s | Charts count: %d",
                d.name,
                d.id,
                d.title,
                len(d.charts),
            )
    except grpc.RpcError as exc:
        logger.error(
            "gRPC call to Dashboard Service failed (endpoint=%s): %s",
            endpoint,
            exc,
        )
    except Exception as exc:  # pylint: disable=broad-except
        logger.error("Unexpected error occurred while listing dashboards: %s", exc)


if __name__ == "__main__":
    app.run(main)
