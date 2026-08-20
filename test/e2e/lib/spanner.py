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

"""Ephemeral Cloud Spanner database provisioning, migrations, and cleanup."""

from __future__ import annotations

import concurrent.futures
import logging
import time

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


class SpannerManager:
    """Manages ephemeral Cloud Spanner databases and migrations."""

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

    def _create_database(self, db_name: str) -> None:
        """Creates a single Spanner database."""
        logger.info("Creating Spanner database %s...", db_name)
        run_cmd(
            [
                "gcloud",
                "spanner",
                "databases",
                "create",
                db_name,
                f"--instance={self.cfg.spanner_instance}",
                f"--project={self.cfg.project_id}",
            ]
        )

    def _apply_migrations(self, db_name: str, mig_dir: str) -> None:
        """Applies schema migrations for a database."""
        logger.info("Applying migrations for %s (%s)...", db_name, mig_dir)
        for attempt in range(1, 4):
            try:
                run_cmd(
                    [
                        "bazel",
                        "run",
                        "//internal/storage/spanner/migrations:migrations",
                        "--",
                        f"-project={self.cfg.project_id}",
                        f"-instance={self.cfg.spanner_instance}",
                        f"-database={db_name}",
                        f"-migrations_dir={mig_dir}",
                        "-command=up",
                    ],
                    cwd=self.cfg.workspace_root,
                )
                break
            except Exception as e:
                if attempt == 3:
                    raise
                logger.warning(
                    "Migration attempt %d failed: %s. Retrying in 5s...",
                    attempt,
                    e,
                )
                time.sleep(5)

    def setup(self) -> None:
        """Creates ephemeral databases and applies migrations."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        targets = [
            (self.cfg.ess_db, "ess"),
            (self.cfg.xid_db, "xid"),
        ]
        # Provision both databases concurrently via gcloud
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor:
            futures = [executor.submit(self._create_database, db_name) for db_name, _ in targets]
            for future in concurrent.futures.as_completed(futures):
                future.result()

        # Apply migrations sequentially to prevent Bazel server output base lock contention
        for db_name, mig_dir in targets:
            self._apply_migrations(db_name, mig_dir)

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
