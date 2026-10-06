#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Inspects XMC Lite experiments stored in ~/.xmanager/experiments.sqlite3."""

import contextlib
import json
import os
import sqlite3
from collections.abc import Sequence
from dataclasses import asdict, dataclass
from typing import Any

from absl import app, flags
from google.protobuf import text_format
from xmanager.generated import data_pb2

_DB_PATH = flags.DEFINE_string(
    "db_path",
    os.path.expanduser("~/.xmanager/experiments.sqlite3"),
    "Path to the local XManager SQLite database file.",
)
_EXPERIMENT_ID = flags.DEFINE_integer("experiment_id", None, "Optional experiment_id filter.")


@dataclass(frozen=True)
class StoredKubernetesJobRow:
    """Represents a decoded Kubernetes job row from the local SQLite database."""

    experiment_id: int
    experiment_title: str
    work_unit_id: int
    job_name: str
    k8s_namespace: str
    k8s_job_name: str
    raw_job_data: str


def _row_to_stored_job(row: tuple[Any, ...]) -> StoredKubernetesJobRow:
    """Converts a raw SQLite tuple into a decoded StoredKubernetesJobRow."""
    exp_id, title, wu_id, job_name, raw_data = row
    job_proto = text_format.Parse(str(raw_data), data_pb2.Job())
    ns = job_proto.kubernetes.namespace if job_proto.HasField("kubernetes") else ""
    k8s_name = job_proto.kubernetes.job_name if job_proto.HasField("kubernetes") else ""
    return StoredKubernetesJobRow(
        experiment_id=int(exp_id),
        experiment_title=str(title),
        work_unit_id=int(wu_id),
        job_name=str(job_name),
        k8s_namespace=ns,
        k8s_job_name=k8s_name,
        raw_job_data=str(raw_data),
    )


def read_sqlite_experiments(
    db_path: str,
    experiment_id: int | None = None,
) -> list[StoredKubernetesJobRow]:
    """Reads experiment, work_unit, and job records from experiments.sqlite3.

    Args:
      db_path: Path to the SQLite database file.
      experiment_id: Optional experiment_id to filter results.

    Returns:
      A list of StoredKubernetesJobRow records ordered by experiment and work unit.
    """
    query = (
        "SELECT e.experiment_id, e.experiment_title, w.work_unit_id, "
        "j.job_name, j.job_data FROM experiment e "
        "JOIN work_unit w ON e.experiment_id = w.experiment_id "
        "JOIN job j ON w.experiment_id = j.experiment_id "
        "AND w.work_unit_id = j.work_unit_id"
    )
    params: tuple[int, ...] = (experiment_id,) if experiment_id is not None else ()
    if experiment_id is not None:
        query += " WHERE e.experiment_id = ?"
    query += " ORDER BY e.experiment_id DESC, w.work_unit_id ASC, j.job_name ASC"
    if not os.path.isfile(db_path):
        raise FileNotFoundError(f"SQLite database file not found at: {db_path}")
    with contextlib.closing(sqlite3.connect(db_path)) as conn:
        rows = conn.execute(query, params).fetchall()
    return [_row_to_stored_job(row) for row in rows]


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint to print decoded SQLite records as JSON."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")
    try:
        records = read_sqlite_experiments(_DB_PATH.value, _EXPERIMENT_ID.value)
    except FileNotFoundError as e:
        raise app.UsageError(str(e)) from e
    print(json.dumps([asdict(r) for r in records], indent=2))


if __name__ == "__main__":
    app.run(main)
