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

"""Ephemeral Cloud Spanner database provisioning, schema initialization, and cleanup."""

from __future__ import annotations

import concurrent.futures
import logging
import os
import time

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


class SpannerManager:
    """Manages ephemeral Cloud Spanner databases and schema provisioning."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config

    def _cleanup_stale_databases(self) -> None:
        """Deletes stale ephemeral test databases to stay under quota limits."""
        logger.info(
            "Checking for stale ephemeral databases on instance %s...",
            self.cfg.spanner_instance,
        )
        try:
            res = run_cmd(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "list",
                    f"--instance={self.cfg.spanner_instance}",
                    f"--project={self.cfg.project_id}",
                    "--format=value(NAME)",
                ],
                capture_output=True,
                check=True,
            )
            current_ts = int(time.time())
            for line in res.stdout.splitlines():
                db_path = line.strip()
                db_id = db_path.split("/")[-1]
                if not db_id.startswith("e2e-ess-") and not db_id.startswith("e2e-xid-"):
                    continue
                try:
                    parts = db_id.split("-")
                    if len(parts) >= 3:
                        ts_part = int(parts[2])
                    else:
                        continue
                    # If older than 1 hour (3600s), remove it
                    if current_ts - ts_part > 3600:
                        logger.info("Cleaning up stale database: %s", db_id)
                        run_cmd(
                            [
                                "gcloud",
                                "spanner",
                                "databases",
                                "delete",
                                db_id,
                                f"--instance={self.cfg.spanner_instance}",
                                f"--project={self.cfg.project_id}",
                                "--quiet",
                            ],
                            check=False,
                        )
                except ValueError:
                    continue
        except Exception as e:
            logger.warning("Stale database cleanup failed: %s", e)

    def _create_database(self, db_name: str, ddl_file: str) -> None:
        """Creates a single Spanner database and applies its schema via DDL file."""
        ddl_path = os.path.join(self.cfg.workspace_root, ddl_file)
        logger.info(
            "Creating Spanner database %s with schema from %s...",
            db_name,
            ddl_file,
        )
        run_cmd(
            [
                "gcloud",
                "spanner",
                "databases",
                "create",
                db_name,
                f"--instance={self.cfg.spanner_instance}",
                f"--project={self.cfg.project_id}",
                f"--ddl-file={ddl_path}",
            ]
        )

    def setup(self) -> None:
        """Creates ephemeral databases and initializes schemas concurrently."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        targets = [
            (
                self.cfg.ess_db,
                "internal/storage/spanner/schema/ess-schema.sql",
            ),
            (
                self.cfg.xid_db,
                "internal/storage/spanner/schema/xid-schema.sql",
            ),
        ]
        # Provision both databases concurrently with full schema DDL in a single batch
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor:
            futures = [
                executor.submit(self._create_database, db_name, ddl_file)
                for db_name, ddl_file in targets
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()

    def _drop_database(self, db_name: str) -> None:
        """Drops a single Spanner database safely."""
        try:
            logger.info("Dropping Spanner database %s...", db_name)
            run_cmd(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "delete",
                    db_name,
                    f"--instance={self.cfg.spanner_instance}",
                    f"--project={self.cfg.project_id}",
                    "--quiet",
                ],
                check=False,
            )
        except Exception as e:
            logger.warning("Failed to delete database %s: %s", db_name, e)

    def teardown(self) -> None:
        """Drops ephemeral databases safely in parallel."""
        logger.info("=== [Spanner] Cleaning up ephemeral databases ===")
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            futures = [
                executor.submit(self._drop_database, db_name)
                for db_name in [self.cfg.ess_db, self.cfg.xid_db]
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()
