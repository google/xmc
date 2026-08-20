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
                check=False,
            )
            current_ts = int(time.time())
            for line in res.stdout.splitlines():
                db = line.strip()
                if not db.startswith("e2e-ess-") and not db.startswith("e2e-xid-"):
                    continue
                try:
                    parts = db.split("-")
                    if len(parts) >= 3:
                        ts_part = int(parts[2])
                    else:
                        continue
                    # If older than 1 hour (3600s), remove it
                    if current_ts - ts_part > 3600:
                        logger.info("Cleaning up stale database: %s", db)
                        run_cmd(
                            [
                                "gcloud",
                                "spanner",
                                "databases",
                                "delete",
                                db,
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

    def setup(self) -> None:
        """Creates ephemeral databases and runs migrations."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        for db_name, mig_dir in [
            (self.cfg.ess_db, "ess"),
            (self.cfg.xid_db, "xid"),
        ]:
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

    def teardown(self) -> None:
        """Drops ephemeral databases safely."""
        logger.info("=== [Spanner] Cleaning up ephemeral databases ===")
        for db_name in [self.cfg.ess_db, self.cfg.xid_db]:
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
                logger.warning("Failed to drop database %s: %s", db_name, e)
