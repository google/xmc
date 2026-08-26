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

"""Cloud Spanner database lifecycle and provisioning manager for local dev."""

from __future__ import annotations

import concurrent.futures
from pathlib import Path

from tools.common.tool_utils import log_info, log_success, run_command
from tools.dev_env.config import LocalDevConfig

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent

_SCHEMA_DEFINITIONS: dict[str, tuple[str, str]] = {
    "ess": (
        "internal/storage/spanner/schema/ess-schema.sql",
        "bazel-bin/api/protos/experiment/experiment_messages_descriptor.pb",
    ),
    "xid": (
        "internal/storage/spanner/schema/xid-schema.sql",
        "bazel-bin/api/protos/xid/xid_messages_descriptor.pb",
    ),
}


class SpannerManager:
    """Manages Cloud Spanner database provisioning and teardown."""

    def __init__(self, config: LocalDevConfig, repo_root: Path | None = None) -> None:
        """Initializes SpannerManager with development configuration."""
        self.config = config
        self.repo_root = repo_root or _REPO_ROOT

    def list_databases(self, instance: str | None = None) -> list[str]:
        """Lists existing database names on the configured Spanner instance."""
        target_instance = instance or self.config.spanner_instance
        log_info(
            f"Listing databases on Spanner instance '{target_instance}' "
            f"(project: '{self.config.project_id}')..."
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
            check=False,
            log_cmd=False,
        )
        if res.returncode != 0:
            log_info("Could not list databases; instance may not exist or permissions are pending.")
            return []
        return [line.strip().split("/")[-1] for line in res.stdout.splitlines() if line.strip()]

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
        if ddl_file and proto_desc_file:
            ddl_path = self.repo_root / ddl_file
            proto_path = self.repo_root / proto_desc_file
            if ddl_path.exists() and proto_path.exists():
                log_info(
                    f"Creating Spanner database '{db_name}' with schema '{ddl_file}' and descriptors '{proto_desc_file}'..."
                )
                cmd.extend([f"--ddl-file={ddl_path}", f"--proto-descriptors-file={proto_path}"])
            else:
                log_info(f"Creating Spanner database '{db_name}'...")
        else:
            log_info(f"Creating Spanner database '{db_name}'...")
        run_command(cmd)

    def ensure_databases_exist(
        self,
        instance: str | None = None,
        databases: list[tuple[str, str, str]] | None = None,
    ) -> set[str]:
        """Ensures databases exist, provisioning missing ones with schema in a single batch."""
        target_instance = instance or self.config.spanner_instance
        existing_dbs = set(self.list_databases(instance=target_instance))

        db_targets = databases or [
            (getattr(self.config, f"{key}_database"), ddl, proto)
            for key, (ddl, proto) in _SCHEMA_DEFINITIONS.items()
            if getattr(self.config, f"{key}_database", None)
        ]
        missing_targets = [t for t in db_targets if t[0] not in existing_dbs]
        if not missing_targets:
            log_info("Required Spanner databases already exist.")
            return set()

        missing_names = [t[0] for t in missing_targets]
        log_info(f"Provisioning missing Spanner databases: {', '.join(missing_names)}...")
        try:
            self._build_protobuf_descriptors()
        except Exception as e:
            log_info(
                f"Protobuf descriptor build warning: {e}. Falling back to standard database creation."
            )

        with concurrent.futures.ThreadPoolExecutor(max_workers=len(missing_targets)) as executor:
            futures = [
                executor.submit(self._create_database, target_instance, db_name, ddl, proto)
                for db_name, ddl, proto in missing_targets
            ]
            for future in concurrent.futures.as_completed(futures):
                future.result()

        log_success(f"Spanner databases created: {', '.join(missing_names)}")
        return set(missing_names)

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
            check=False,
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
