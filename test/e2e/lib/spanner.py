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
import logging
import os
import threading
import time

from test.e2e.lib.command import run_cmd
from test.e2e.lib.config import E2EConfig

logger = logging.getLogger("xmc_e2e_runner")


def _extract_error_details(e: Exception) -> str:
    """Extracts combined stderr, stdout, and message from exception in lowercase."""
    stdout = getattr(e, "stdout", "") or getattr(e, "output", "") or ""
    stderr = getattr(e, "stderr", "") or ""
    if isinstance(stdout, bytes):
        stdout = stdout.decode("utf-8", errors="ignore")
    if isinstance(stderr, bytes):
        stderr = stderr.decode("utf-8", errors="ignore")
    return f"{stderr}\n{stdout}\n{e}".lower()


def _is_retryable_spanner_error(err_str: str) -> bool:
    """Returns True if error string contains transient Spanner quota or rate limit errors."""
    retryable_markers = (
        "resource_exhausted",
        "resourceexhausted",
        "quota exceeded",
        "rate limit",
        "429",
        "503",
        "unavailable",
        "504",
        "deadline_exceeded",
        "deadlineexceeded",
        "aborted",
        "409",
        "maximum database limit",
        "too many concurrent",
        "pending ddl operations",
    )
    return any(marker in err_str for marker in retryable_markers)


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
                for root, _, files in os.walk(mig_dir, followlinks=True):
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

    def _create_database(self, db_name: str, ddl_file: str, proto_desc_file: str) -> None:
        """Creates a single Spanner database with retries on RESOURCE_EXHAUSTED / 429."""
        ddl_path = (
            ddl_file if os.path.isabs(ddl_file) else os.path.join(self.cfg.workspace_root, ddl_file)
        )
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
                err_str = _extract_error_details(e)
                if _is_retryable_spanner_error(err_str) and attempt < max_attempts:
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

    _bazel_run_lock = threading.Lock()

    def _init_migration_version(self, db_name: str, mig_dir: str) -> None:
        """Initializes SchemaMigrations version tracking table without re-running DDLs."""
        logger.info("Initializing migration version for %s (%s)...", db_name, mig_dir)
        mig_binary = self._resolve_migration_binary()
        if not mig_binary:
            lock = self._bazel_run_lock
            mig_cmd = [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
            ]
        else:
            lock = contextlib.nullcontext()
            mig_cmd = [mig_binary]

        max_attempts = 5
        base_delay = 5
        for attempt in range(1, max_attempts + 1):
            try:
                with lock:
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
                err_str = _extract_error_details(e)
                if _is_retryable_spanner_error(err_str) and attempt < max_attempts:
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
