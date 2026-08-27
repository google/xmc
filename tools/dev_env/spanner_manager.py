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

"""Cloud Spanner database lifecycle and schema migration manager for local dev."""

from __future__ import annotations

import concurrent.futures
import logging
import re
import time
from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig

_LOGGER = logging.getLogger("xmc.dev_env.spanner")

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

_SCHEMA_DEFINITIONS: dict[str, tuple[str, str, str]] = {
    "ess": (
        "internal/storage/spanner/schema/ess-schema.sql",
        "bazel-bin/api/protos/experiment/experiment_messages_descriptor.pb",
        "ess",
    ),
    "xid": (
        "internal/storage/spanner/schema/xid-schema.sql",
        "bazel-bin/api/protos/xid/xid_messages_descriptor.pb",
        "xid",
    ),
}


class SpannerManager:
    """Manages Cloud Spanner database provisioning, migrations, and teardown."""

    def __init__(self, config: LocalDevConfig, repo_root: Path | None = None) -> None:
        """Initializes SpannerManager with development configuration."""
        self.config = config
        self.repo_root = repo_root or _REPO_ROOT

    def list_databases(self, instance: str | None = None) -> list[str]:
        """Lists existing database names on the configured Spanner instance."""
        target_instance = instance or self.config.spanner_instance
        _LOGGER.debug(
            "Listing databases on Spanner instance '%s' (project: '%s')",
            target_instance,
            self.config.project_id,
        )
        res = run_command(
            [
                "gcloud",
                "spanner",
                "databases",
                "list",
                f"--instance={target_instance}",
                f"--project={self.config.project_id}",
                "--format=value(NAME)",
            ],
            capture_output=True,
            check=True,
            log_cmd=False,
        )
        dbs = [line.strip().split("/")[-1] for line in res.stdout.splitlines() if line.strip()]
        _LOGGER.debug("Found Spanner databases on instance '%s': %s", target_instance, dbs)
        return dbs

    def _build_protobuf_descriptors(self) -> None:
        """Builds protobuf descriptor sets required for Spanner PROTO BUNDLE."""
        log_info("Building protobuf descriptor sets with Bazel...")
        run_command(
            [
                "bazel",
                "build",
                "//api/protos/experiment:experiment_messages_descriptor",
                "//api/protos/xid:xid_messages_descriptor",
            ],
            cwd=str(self.repo_root),
        )

    def _create_database(
        self,
        instance: str,
        db_name: str,
        ddl_file: str | None = None,
        proto_desc_file: str | None = None,
    ) -> None:
        """Creates a single Spanner database, applying schema DDL and descriptors if provided."""
        cmd = [
            "gcloud",
            "spanner",
            "databases",
            "create",
            db_name,
            f"--instance={instance}",
            f"--project={self.config.project_id}",
        ]
        log_msg = f"Creating Spanner database '{db_name}'"
        if ddl_file:
            ddl_path = self.repo_root / ddl_file
            if not ddl_path.exists():
                raise FileNotFoundError(f"DDL schema file not found: {ddl_path}")
            cmd.append(f"--ddl-file={ddl_path}")
            log_msg += f" with schema '{ddl_file}'"
        if proto_desc_file:
            proto_path = self.repo_root / proto_desc_file
            if not proto_path.exists():
                raise FileNotFoundError(f"Protobuf descriptor file not found: {proto_path}")
            cmd.append(f"--proto-descriptors-file={proto_path}")
            log_msg += f" and descriptors '{proto_desc_file}'"
        log_info(f"{log_msg}...")
        run_command(cmd)

    def _init_migration_version(self, instance: str, db_name: str, mig_dir: str) -> None:
        """Initializes SchemaMigrations version tracking table for newly created database."""
        log_info(f"Initializing migration version tracking for '{db_name}' (dir: '{mig_dir}')...")
        run_command(
            [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
                f"-project={self.config.project_id}",
                f"-instance={instance}",
                f"-database={db_name}",
                f"-migrations_dir={mig_dir}",
                "-command=init",
            ],
            cwd=str(self.repo_root),
        )

    def ensure_databases_exist(
        self,
        instance: str | None = None,
        databases: list[tuple[str, str, str] | tuple[str, str, str, str]] | None = None,
    ) -> set[str]:
        """Ensures databases exist, provisioning missing ones with schema in a single batch."""
        target_instance = instance or self.config.spanner_instance
        existing_dbs = set(self.list_databases(instance=target_instance))

        db_targets = databases or [
            (getattr(self.config, f"{key}_database"), ddl, proto, mig)
            for key, (ddl, proto, mig) in _SCHEMA_DEFINITIONS.items()
            if getattr(self.config, f"{key}_database", None)
        ]
        normalized_targets: list[tuple[str, str, str, str]] = []
        for target in db_targets:
            if len(target) == 3:
                normalized_targets.append((target[0], target[1], target[2], ""))
            else:
                normalized_targets.append(target)
        missing_targets = [t for t in normalized_targets if t[0] not in existing_dbs]
        if not missing_targets:
            _LOGGER.debug("Required Spanner databases already exist: %s", existing_dbs)
            return set()

        missing_names = [t[0] for t in missing_targets]
        log_info(f"Provisioning missing Spanner databases: {', '.join(missing_names)}...")
        if any(t[2] for t in missing_targets):
            self._build_protobuf_descriptors()

        with concurrent.futures.ThreadPoolExecutor(max_workers=len(missing_targets)) as executor:
            futures = [
                executor.submit(self._create_database, target_instance, db_name, ddl, proto)
                for db_name, ddl, proto, _ in missing_targets
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()

        for db_name, _, _, mig_dir in missing_targets:
            if mig_dir:
                self._init_migration_version(target_instance, db_name, mig_dir)

        log_success(f"Spanner databases created: {', '.join(missing_names)}")
        return set(missing_names)

    def get_migration_version(
        self,
        instance: str | None = None,
        database: str = "",
        migrations_dir: str = "",
    ) -> tuple[int, bool]:
        """Inspects current database schema migration version."""
        if not database:
            raise ValueError("database must be specified")
        if not migrations_dir:
            raise ValueError("migrations_dir must be specified")

        target_instance = instance or self.config.spanner_instance
        res = run_command(
            [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
                f"-project={self.config.project_id}",
                f"-instance={target_instance}",
                f"-database={database}",
                f"-migrations_dir={migrations_dir}",
                "-command=version",
            ],
            cwd=str(self.repo_root),
            capture_output=True,
            check=False,
        )
        output = (res.stdout or "") + (res.stderr or "")
        if res.returncode != 0:
            raise ValueError(
                f"Failed to get migration version for database '{database}': {output.strip()}"
            )

        ver_match = re.search(r"Version:\s*([0-9]+)", output, re.IGNORECASE)
        current_version = int(ver_match.group(1)) if ver_match else 0
        dirty_match = re.search(r"Dirty:\s*(true|false)", output, re.IGNORECASE)
        is_dirty = dirty_match.group(1).lower() == "true" if dirty_match else False
        return current_version, is_dirty

    def _apply_database_migration(
        self, instance: str, db_name: str, mig_dir: str, max_attempts: int = 3
    ) -> None:
        """Applies schema migrations for a single database with retries."""
        log_info(f"Applying schema migrations for '{db_name}' (dir: '{mig_dir}')...")
        for attempt in range(1, max_attempts + 1):
            try:
                run_command(
                    [
                        "bazel",
                        "run",
                        "//internal/storage/spanner/migrations:migrations",
                        "--",
                        f"-project={self.config.project_id}",
                        f"-instance={instance}",
                        f"-database={db_name}",
                        f"-migrations_dir={mig_dir}",
                        "-command=up",
                    ],
                    cwd=str(self.repo_root),
                    log_on_error=(attempt == max_attempts),
                )
                return
            except Exception as e:
                if attempt == max_attempts:
                    raise ValueError(
                        f"Migration failed for '{db_name}' after {max_attempts} attempts: {e}"
                    ) from e
                log_info(f"Migration attempt {attempt} failed, retrying in 5s...")
                time.sleep(5)

    def apply_migrations(
        self,
        instance: str | None = None,
        database: str | None = None,
        migrations_dir: str | None = None,
        newly_created: set[str] | None = None,
        force: bool = False,
    ) -> None:
        """Executes database schema migrations for configured databases."""
        if database:
            if not migrations_dir:
                for key, (_, _, m_dir) in _SCHEMA_DEFINITIONS.items():
                    if getattr(self.config, f"{key}_database", None) == database:
                        migrations_dir = m_dir
                        break
            if not migrations_dir:
                raise ValueError(
                    f"Could not resolve migrations directory for database '{database}'"
                )
            targets = [(database, migrations_dir)]
        elif migrations_dir:
            raise ValueError("database must be specified if migrations_dir is specified")
        else:
            targets = [
                (getattr(self.config, f"{key}_database"), mig_dir)
                for key, (_, _, mig_dir) in _SCHEMA_DEFINITIONS.items()
                if getattr(self.config, f"{key}_database", None)
            ]

        if not force and not self.config.auto_migrate:
            _LOGGER.debug("Skipping schema migrations (auto_migrate is disabled).")
            return

        target_instance = instance or self.config.spanner_instance
        newly_created_set = newly_created or set()

        pending = [t for t in targets if t[0] and t[0] not in newly_created_set]
        if not pending:
            _LOGGER.debug("All Spanner databases are up to date (no pending migrations).")
            return

        log_info("Running Spanner schema migrations for existing databases...")
        for db_name, mig_dir in pending:
            self._apply_database_migration(target_instance, db_name, mig_dir)
        log_success("All Spanner schema migrations applied successfully.")

    def setup(self) -> None:
        """Executes full Spanner setup: database creation and schema migrations."""
        log_info(
            f"=== [Spanner] Setting up databases on instance '{self.config.spanner_instance}' ==="
        )
        newly_created = self.ensure_databases_exist()
        self.apply_migrations(newly_created=newly_created)

    def _drop_database(self, instance: str, db_name: str) -> None:
        """Safely drops a single Spanner database."""
        log_info(f"Dropping Spanner database '{db_name}'...")
        run_command(
            [
                "gcloud",
                "spanner",
                "databases",
                "delete",
                db_name,
                f"--instance={instance}",
                f"--project={self.config.project_id}",
                "--quiet",
            ],
            check=True,
        )

    def drop_databases(
        self,
        instance: str | None = None,
        databases: list[str] | None = None,
    ) -> None:
        """Drops development databases in parallel."""
        target_instance = instance or self.config.spanner_instance
        target_dbs = databases or [
            db for db in [self.config.ess_database, self.config.xid_database] if db
        ]
        if not target_dbs:
            return

        log_info(
            f"=== [Spanner] Dropping databases on instance '{target_instance}': {', '.join(target_dbs)} ==="
        )
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(target_dbs)) as executor:
            futures = [
                executor.submit(self._drop_database, target_instance, db) for db in target_dbs
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()

        log_success(f"Spanner databases deleted: {', '.join(target_dbs)}")
