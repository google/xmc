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
import contextlib
import functools
import logging
import os
import subprocess
import threading
import time

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.spanner_retry import (
    RetryBudget,
    is_already_exists_error,
    run_with_transient_retry,
)

logger = logging.getLogger("xmc_e2e_runner")

# Database creation: 4 attempts with waits of 15, 30, and 60 s (each +-20%), every attempt
# bounded to 120 s and the whole loop to 240 s.
CREATE_DATABASE_RETRY = RetryBudget(
    max_attempts=4,
    base_delay_seconds=15,
    attempt_timeout_seconds=120,
    total_seconds=240,
    jitter_fraction=0.2,
)
# Migration version initialization: 3 attempts with waits of 5 and 10 s, every attempt bounded
# to 45 s and the whole loop to 90 s, so that a first attempt that times out still leaves 40 s
# for a retry, above the 30 s MIN_RETRY_ATTEMPT_SECONDS floor.
MIGRATION_INIT_RETRY = RetryBudget(
    max_attempts=3, base_delay_seconds=5, attempt_timeout_seconds=45, total_seconds=90
)
# How often a retried creation that found its database still being created polls its state.
_READY_POLL_SECONDS = 5

_SCHEMA_MIGRATIONS_DDL = (
    "\n\nCREATE TABLE SchemaMigrations (\n"
    "  Version INT64 NOT NULL,\n"
    "  Dirty BOOL NOT NULL,\n"
    ") PRIMARY KEY(Version);\n"
)


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

    def _get_bazel_bin(self) -> str:
        """Resolves the actual bazel-bin directory path dynamically."""
        if getattr(self, "_cached_bazel_bin", None) is not None:
            return self._cached_bazel_bin
        try:
            res = run_cmd(
                ["bazel", "info", "bazel-bin"], cwd=self.cfg.workspace_root, capture_output=True
            )
            self._cached_bazel_bin = res.stdout.strip()
        except Exception:
            self._cached_bazel_bin = os.path.join(self.cfg.workspace_root, "bazel-bin")
        return self._cached_bazel_bin

    def _resolve_migration_binary(self) -> str | None:
        """Resolves path to compiled migrations binary under bazel-bin."""
        if getattr(self, "_cached_mig_binary", None) is not None:
            return self._cached_mig_binary

        binary_names = ("migrations.exe", "migrations") if os.name == "nt" else ("migrations",)
        search_dirs = [
            os.path.join(
                self.cfg.workspace_root, "bazel-bin", "internal", "storage", "spanner", "migrations"
            ),
            os.path.join(self._get_bazel_bin(), "internal", "storage", "spanner", "migrations"),
        ]

        for mig_dir in search_dirs:
            for name in binary_names:
                mig_binary = os.path.join(mig_dir, "migrations_", name)
                if os.path.isfile(mig_binary) and os.access(mig_binary, os.X_OK):
                    self._cached_mig_binary = mig_binary
                    return mig_binary

            if os.path.isdir(mig_dir):
                for root, _, files in os.walk(mig_dir):
                    for f in files:
                        if f in binary_names:
                            candidate = os.path.join(root, f)
                            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                                self._cached_mig_binary = candidate
                                return candidate
        return None

    def _resolve_descriptor(self, pkg: str, filename: str) -> str:
        """Resolves path to compiled proto descriptor file."""
        candidates = [
            os.path.join(self.cfg.workspace_root, "bazel-bin", "api", "protos", pkg, filename),
            os.path.join(self._get_bazel_bin(), "api", "protos", pkg, filename),
        ]
        for c in candidates:
            if os.path.isfile(c) and os.path.getsize(c) > 0:
                return c
        return candidates[0]

    def _build_prerequisites(self) -> None:
        """Builds protobuf descriptor sets and migration binary required for Spanner if not already present."""
        ess_desc = self._resolve_descriptor("experiment", "experiment_messages_descriptor.pb")
        xid_desc = self._resolve_descriptor("xid", "xid_messages_descriptor.pb")
        mig_binary = self._resolve_migration_binary()
        targets = []
        ess_valid = os.path.isfile(ess_desc) and os.path.getsize(ess_desc) > 0
        xid_valid = os.path.isfile(xid_desc) and os.path.getsize(xid_desc) > 0
        if not (ess_valid and xid_valid):
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

    def _prepare_e2e_ddl_file(self, db_name: str, ddl_path: str) -> str:
        """Creates a temporary DDL file appending SchemaMigrations for fast single-batch init."""
        try:
            with open(ddl_path, encoding="utf-8") as f:
                schema_sql = f.read()
        except OSError:
            return ddl_path

        if "CREATE TABLE SchemaMigrations" in schema_sql:
            return ddl_path

        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        combined_ddl_path = os.path.join(self.cfg.temp_dir, f"{db_name}-schema.sql")
        with open(combined_ddl_path, "w", encoding="utf-8") as f:
            f.write(schema_sql + _SCHEMA_MIGRATIONS_DDL)
        return combined_ddl_path

    def _databases_cmd(self, verb: str, db_name: str, *flags: str) -> list[str]:
        """Builds a ``gcloud spanner databases`` command for a database on the test instance."""
        return [
            "gcloud",
            "spanner",
            "databases",
            verb,
            db_name,
            f"--instance={self.cfg.spanner_instance}",
            f"--project={self.cfg.project_id}",
            *flags,
        ]

    def _create_database(self, db_name: str, ddl_file: str, proto_desc_file: str) -> None:
        """Creates a single Spanner database, retrying transient errors with backoff."""
        ddl_path = (
            ddl_file if os.path.isabs(ddl_file) else os.path.join(self.cfg.workspace_root, ddl_file)
        )
        effective_ddl_path = self._prepare_e2e_ddl_file(db_name, ddl_path)
        proto_desc_path = (
            proto_desc_file
            if os.path.isabs(proto_desc_file)
            else os.path.join(self.cfg.workspace_root, proto_desc_file)
        )
        logger.info(
            "Creating Spanner database %s with schema from %s and descriptors from %s...",
            db_name,
            ddl_file,
            proto_desc_file,
        )
        create_cmd = self._databases_cmd(
            "create",
            db_name,
            f"--ddl-file={effective_ddl_path}",
            f"--proto-descriptors-file={proto_desc_path}",
        )
        run_with_transient_retry(
            functools.partial(self._create_database_once, create_cmd, db_name),
            f"Creating Spanner database {db_name}",
            CREATE_DATABASE_RETRY,
        )

    def _create_database_once(
        self, create_cmd: list[str], db_name: str, attempt: int, timeout: float
    ) -> None:
        """Runs one create attempt; a retried create succeeds once the database is READY.

        An earlier attempt that timed out or failed transiently may still have created the
        database, in which case the retried create fails with ALREADY_EXISTS while the
        database is often still CREATING. Its state is then polled for the rest of the
        attempt.
        """
        started = time.monotonic()
        try:
            run_cmd(create_cmd, capture_output=True, timeout=timeout)
        except subprocess.CalledProcessError as exc:
            if attempt == 1 or not is_already_exists_error(exc):
                raise
            self._wait_until_ready(db_name, timeout - (time.monotonic() - started))
            logger.info("Spanner database %s already exists and is READY.", db_name)

    def _wait_until_ready(self, db_name: str, timeout: float) -> None:
        """Polls the state of the database until it is READY, for at most ``timeout`` seconds.

        Raises:
            subprocess.TimeoutExpired: If the database is not READY in time, which the retry
                loop treats as transient.
        """
        describe_cmd = self._databases_cmd("describe", db_name, "--format=value(state)")
        deadline = time.monotonic() + timeout
        state = "unknown"
        while (remaining := deadline - time.monotonic()) > 0:
            res = run_cmd(describe_cmd, capture_output=True, check=False, timeout=remaining)
            state = res.stdout.strip() if res.returncode == 0 else "unknown"
            if state == "READY":
                return
            time.sleep(min(_READY_POLL_SECONDS, max(0.0, deadline - time.monotonic())))
        raise subprocess.TimeoutExpired(
            describe_cmd, timeout, stderr=f"Spanner database {db_name} is {state}, not READY."
        )

    _bazel_run_lock = threading.Lock()

    def _init_migration_version(self, db_name: str, mig_dir: str) -> None:
        """Initializes SchemaMigrations version tracking table without re-running DDLs."""
        logger.info("Initializing migration version for %s (%s)...", db_name, mig_dir)
        mig_binary = self._resolve_migration_binary()
        if not mig_binary:
            lock = self._bazel_run_lock
            mig_cmd = ["bazel", "run", "//internal/storage/spanner/migrations:migrations", "--"]
        else:
            lock = contextlib.nullcontext()
            mig_cmd = [mig_binary]
        init_cmd = [
            *mig_cmd,
            f"-project={self.cfg.project_id}",
            f"-instance={self.cfg.spanner_instance}",
            f"-database={db_name}",
            f"-migrations_dir={mig_dir}",
            "-command=init",
        ]

        def _init_once(_attempt: int, timeout: float) -> None:
            with lock:
                run_cmd(init_cmd, cwd=self.cfg.workspace_root, capture_output=True, timeout=timeout)

        run_with_transient_retry(
            _init_once, f"Initializing migration version for {db_name}", MIGRATION_INIT_RETRY
        )

    def setup(self) -> None:
        """Creates ephemeral databases and initializes schemas concurrently."""
        logger.info("=== [Spanner] Setting up ephemeral databases ===")
        self._cleanup_stale_databases()
        self._build_prerequisites()
        targets = [
            (
                self.cfg.ess_db,
                "internal/storage/spanner/schema/ess-schema.sql",
                self._resolve_descriptor("experiment", "experiment_messages_descriptor.pb"),
                "ess",
            ),
            (
                self.cfg.xid_db,
                "internal/storage/spanner/schema/xid-schema.sql",
                self._resolve_descriptor("xid", "xid_messages_descriptor.pb"),
                "xid",
            ),
        ]
        # Provision both databases concurrently with full schema DDL in a single batch
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor1:
            futures = [
                executor1.submit(self._create_database, db_name, ddl_file, proto_file)
                for db_name, ddl_file, proto_file, _ in targets
            ]
            try:
                for future in concurrent.futures.as_completed(futures):
                    future.result()
            except BaseException:
                for future in futures:
                    future.cancel()
                raise

        # Initialize SchemaMigrations version tracking table concurrently for both databases
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(targets)) as executor2:
            futures = [
                executor2.submit(self._init_migration_version, db_name, mig_dir)
                for db_name, _, _, mig_dir in targets
            ]
            try:
                for future in concurrent.futures.as_completed(futures):
                    future.result()
            except BaseException:
                for future in futures:
                    future.cancel()
                raise

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
            try:
                for future in concurrent.futures.as_completed(futures):
                    future.result()
            except BaseException:
                for future in futures:
                    future.cancel()
                raise
