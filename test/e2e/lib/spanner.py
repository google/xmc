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

    def _resolve_migration_binary(self) -> str | None:
        """Resolves path to compiled migrations binary under bazel-bin."""
        mig_dir = os.path.join(
            self.cfg.workspace_root,
            "bazel-bin",
            "internal",
            "storage",
            "spanner",
            "migrations",
        )
        mig_binary = os.path.join(mig_dir, "migrations_", "migrations")
        if os.path.isfile(mig_binary):
            return mig_binary
        if os.path.isdir(mig_dir):
            for root, _, files in os.walk(mig_dir):
                for f in files:
                    if f in ("migrations", "migrations.exe"):
                        candidate = os.path.join(root, f)
                        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                            return candidate
        return None

    def _build_prerequisites(self) -> None:
        """Builds protobuf descriptor sets and migration binary required for Spanner if not already present."""
        ess_desc = os.path.join(
            self.cfg.workspace_root,
            "bazel-bin",
            "api",
            "protos",
            "experiment",
            "experiment_messages_descriptor.pb",
        )
        xid_desc = os.path.join(
            self.cfg.workspace_root,
            "bazel-bin",
            "api",
            "protos",
            "xid",
            "xid_messages_descriptor.pb",
        )
        mig_binary = self._resolve_migration_binary()
        targets = []
        if not (os.path.isfile(ess_desc) and os.path.isfile(xid_desc)):
            targets.extend(
                [
                    "//api/protos/experiment:experiment_messages_descriptor",
                    "//api/protos/xid:xid_messages_descriptor",
                ]
            )
        if not mig_binary:
            targets.append("//internal/storage/spanner/migrations:migrations")

        if not targets:
            logger.info(
                "Protobuf descriptor sets and migration binary already built; skipping Bazel build."
            )
            return

        logger.info("Building prerequisites %s with Bazel...", targets)
        run_cmd(["bazel", "build", *targets], cwd=self.cfg.workspace_root)

    def _create_database(self, db_name: str, ddl_file: str, proto_desc_file: str) -> None:
        """Creates a single Spanner database with retries on RESOURCE_EXHAUSTED / 429."""
        ddl_path = os.path.join(self.cfg.workspace_root, ddl_file)
        proto_desc_path = os.path.join(self.cfg.workspace_root, proto_desc_file)
        logger.info(
            "Creating Spanner database %s with schema from %s and descriptors from %s...",
            db_name,
            ddl_file,
            proto_desc_file,
        )
        max_attempts = 10
        base_delay = 15
        for attempt in range(1, max_attempts + 1):
            try:
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
                        f"--proto-descriptors-file={proto_desc_path}",
                    ],
                    capture_output=True,
                )
                return
            except Exception as e:
                err_raw = getattr(e, "stderr", "") or str(e)
                err_str = (
                    err_raw.decode("utf-8", errors="ignore")
                    if isinstance(err_raw, bytes)
                    else str(err_raw)
                )
                if (
                    "RESOURCE_EXHAUSTED" in err_str
                    or "429" in err_str
                    or "maximum database limit" in err_str
                ) and attempt < max_attempts:
                    delay = min(base_delay * (2 ** (attempt - 1)), 120)
                    logger.warning(
                        "Database creation hit resource quota (attempt %d/%d). "
                        "Waiting %ds for a slot to free up...",
                        attempt,
                        max_attempts,
                        delay,
                    )
                    time.sleep(delay)
                else:
                    raise

    def _init_migration_version(self, db_name: str, mig_dir: str) -> None:
        """Initializes SchemaMigrations version tracking table without re-running DDLs."""
        logger.info("Initializing migration version for %s (%s)...", db_name, mig_dir)
        mig_binary = self._resolve_migration_binary()
        if not mig_binary:
            mig_cmd = [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
            ]
        else:
            mig_cmd = [mig_binary]

        max_attempts = 5
        base_delay = 5
        for attempt in range(1, max_attempts + 1):
            try:
                run_cmd(
                    [
                        *mig_cmd,
                        f"-project={self.cfg.project_id}",
                        f"-instance={self.cfg.spanner_instance}",
                        f"-database={db_name}",
                        f"-migrations_dir={mig_dir}",
                        "-command=init",
                    ],
                    cwd=self.cfg.workspace_root,
                    capture_output=True,
                )
                return
            except Exception as e:
                err_raw = getattr(e, "stderr", "") or str(e)
                err_str = (
                    err_raw.decode("utf-8", errors="ignore")
                    if isinstance(err_raw, bytes)
                    else str(err_raw)
                )
                if (
                    "RESOURCE_EXHAUSTED" in err_str
                    or "429" in err_str
                    or "rate limit" in err_str.lower()
                ) and attempt < max_attempts:
                    delay = min(base_delay * (2 ** (attempt - 1)), 60)
                    logger.warning(
                        "Migration init hit rate limit (attempt %d/%d). Retrying in %ds...",
                        attempt,
                        max_attempts,
                        delay,
                    )
                    time.sleep(delay)
                else:
                    raise

    def setup(self) -> None:
        """Creates ephemeral databases and initializes schemas concurrently."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        self._build_prerequisites()
        targets = [
            (
                self.cfg.ess_db,
                "internal/storage/spanner/schema/ess-schema.sql",
                "bazel-bin/api/protos/experiment/experiment_messages_descriptor.pb",
                "ess",
            ),
            (
                self.cfg.xid_db,
                "internal/storage/spanner/schema/xid-schema.sql",
                "bazel-bin/api/protos/xid/xid_messages_descriptor.pb",
                "xid",
            ),
        ]
        # Provision both databases concurrently with full schema DDL in a single batch
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor:
            futures = [
                executor.submit(self._create_database, db_name, ddl_file, proto_file)
                for db_name, ddl_file, proto_file, _ in targets
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()

        # Initialize SchemaMigrations version tracking table concurrently for both databases
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor:
            futures = [
                executor.submit(self._init_migration_version, db_name, mig_dir)
                for db_name, _, _, mig_dir in targets
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
