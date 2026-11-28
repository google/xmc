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

"""End-to-end verification for examples/mlflow_job across XMC and MLflow APIs.

Verifies:
1. Experiment and N WorkUnits complete on the expected queue.
2. Bidirectional links: XMC Experiment.urls and WorkUnit.urls point to MLflow,
   and MLflow parent/child runs carry xmc.* tags and mlflow.note.content.
3. Run hierarchy: 1 parent run (xmc-<xid>) and N child runs (wu-<wid>), all FINISHED.
4. Optional stop check (--verify_stop): stopping a running WorkUnit transitions
   its MLflow child and parent runs to KILLED.

Usage:
    python3 -m tools.nightly_tests.examples_verification.mlflow_job_test \
      --xid=1 --expected_work_units=4 --mlflow_uri=http://localhost:5000
"""

from __future__ import annotations

import base64
import json
import os
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Sequence
from typing import Any

from absl import flags

from tools.nightly_tests.verification import (
    verifier_cli,
    workload_verification,
    xmc_entities_validator,
)

FLAGS = flags.FLAGS
flags.DEFINE_string(
    "mlflow_uri",
    os.environ.get("MLFLOW_TRACKING_URI", ""),
    "MLflow tracking server base URI (e.g. http://localhost:5000).",
)
flags.DEFINE_string(
    "mlflow_ui_base_url",
    "",
    "Expected MLflow UI base URL in XMC Url chips (defaults to --mlflow_uri).",
)
flags.DEFINE_bool(
    "verify_stop",
    True,
    "Also launch a sleeping WorkUnit, stop it via XMC, and assert KILLED in MLflow.",
)

EXPECTED_TITLE_PATTERN: str = "MLflow Sweep Example"
DEFAULT_WORK_UNITS: int = 4
EXPERIMENT_URL_DISPLAY_NAME: str = "MLflow charts"
WORK_UNIT_URL_DISPLAY_NAME: str = "MLflow run"


def _build_mlflow_headers() -> dict[str, str]:
    """Builds optional Authorization header from MLFLOW_TRACKING_* env vars."""
    headers = {"Content-Type": "application/json"}
    if token := os.environ.get("MLFLOW_TRACKING_TOKEN", "").strip():
        headers["Authorization"] = f"Bearer {token}"
        return headers
    username = os.environ.get("MLFLOW_TRACKING_USERNAME", "").strip()
    password = os.environ.get("MLFLOW_TRACKING_PASSWORD", "").strip()
    if username and password:
        raw = f"{username}:{password}".encode()
        headers["Authorization"] = f"Basic {base64.b64encode(raw).decode('ascii')}"
    return headers


def _mlflow_request(
    base_uri: str,
    method: str,
    path: str,
    body: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Executes a JSON request against the MLflow 3.x REST API."""
    url = f"{base_uri.rstrip('/')}{path}"
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, headers=_build_mlflow_headers(), method=method)
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _get_mlflow_experiment_id(base_uri: str, title: str) -> str:
    """Looks up the MLflow experiment_id by experiment title."""
    encoded = urllib.parse.quote(title, safe="")
    resp = _mlflow_request(
        base_uri,
        "GET",
        f"/api/2.0/mlflow/experiments/get-by-name?experiment_name={encoded}",
    )
    exp_id = str(resp.get("experiment", {}).get("experiment_id", ""))
    if not exp_id:
        raise AssertionError(f"MLflow experiment {title!r} returned empty experiment_id.")
    return exp_id


def _poll_mlflow_experiment_id(base_uri: str, title: str, timeout_sec: int = 120) -> str:
    """Polls MLflow until the experiment is created and returns its ID."""
    deadline = time.monotonic() + timeout_sec
    last_error = "experiment not found"
    while time.monotonic() < deadline:
        try:
            return _get_mlflow_experiment_id(base_uri, title)
        except (AssertionError, urllib.error.URLError, KeyError) as exc:
            last_error = str(exc)
        time.sleep(3)
    raise AssertionError(f"Timed out waiting for MLflow experiment {title!r}: {last_error}")


def _search_mlflow_runs(base_uri: str, exp_id: str, xid: str) -> list[dict[str, Any]]:
    """Returns all MLflow runs tagged with xmc.experiment_id == xid."""
    resp = _mlflow_request(
        base_uri,
        "POST",
        "/api/2.0/mlflow/runs/search",
        body={
            "experiment_ids": [exp_id],
            "filter": f"tags.`xmc.experiment_id` = '{xid}'",
            "max_results": 100,
        },
    )
    return list(resp.get("runs", []))


def _tags_map(run: dict[str, Any]) -> dict[str, str]:
    """Converts MLflow run.data.tags list into a dict."""
    return {t["key"]: t["value"] for t in run.get("data", {}).get("tags", []) if "key" in t}


def _partition_runs(
    runs: list[dict[str, Any]],
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Separates runs into the single parent run and child runs."""
    parents: list[dict[str, Any]] = []
    children: list[dict[str, Any]] = []
    for run in runs:
        tags = _tags_map(run)
        if "mlflow.parentRunId" in tags:
            children.append(run)
        else:
            parents.append(run)
    if len(parents) != 1:
        raise AssertionError(f"Expected 1 MLflow parent run, found {len(parents)}.")
    return parents[0], children


def _assert_url_chip(
    urls: Sequence[Any], display_name: str, expected_substr: str, owner: str
) -> str:
    """Asserts an XMC Url entry exists with display_name and contains expected_substr."""
    for entry in urls:
        if getattr(entry, "display_name", "") == display_name:
            url = getattr(entry, "url", "")
            if expected_substr not in url:
                raise AssertionError(
                    f"{owner} {display_name!r} URL {url!r} missing {expected_substr!r}."
                )
            return url
    raise AssertionError(f"{owner} is missing Url chip {display_name!r}.")


def _assert_run_tags(
    tags: dict[str, str],
    required_keys: Sequence[str],
    xid: str,
    run_label: str,
) -> None:
    """Asserts required XMC tags and Markdown description are present on an MLflow run."""
    for key in required_keys:
        if not tags.get(key):
            raise AssertionError(f"MLflow {run_label} missing tag {key!r}.")
    if tags.get("xmc.experiment_id") != str(xid):
        raise AssertionError(
            f"MLflow {run_label} xmc.experiment_id={tags.get('xmc.experiment_id')!r} != {xid!r}."
        )
    if not tags.get("mlflow.note.content"):
        raise AssertionError(f"MLflow {run_label} missing mlflow.note.content description.")


def _poll_runs_status(
    base_uri: str,
    exp_id: str,
    xid: str,
    expected_children: int,
    expected_status: str,
    timeout_sec: int = 120,
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    """Polls MLflow until parent and all children reach expected_status."""
    deadline = time.monotonic() + timeout_sec
    last_error = "no runs found"
    while time.monotonic() < deadline:
        try:
            runs = _search_mlflow_runs(base_uri, exp_id, xid)
            parent, children = _partition_runs(runs)
            if len(children) != expected_children:
                last_error = f"child count {len(children)} != {expected_children}"
            else:
                statuses = [parent.get("info", {}).get("status")] + [
                    c.get("info", {}).get("status") for c in children
                ]
                if all(s == expected_status for s in statuses):
                    return parent, children
                last_error = f"statuses={statuses} != {expected_status}"
        except (AssertionError, urllib.error.URLError, KeyError) as exc:
            last_error = str(exc)
        time.sleep(3)
    raise AssertionError(
        f"Timed out waiting for MLflow runs of XID {xid} to reach {expected_status}: {last_error}"
    )


def _assert_completed_mlflow_state(stub: Any, xid: str, expected_wus: int, mlflow_uri: str) -> None:
    """Asserts XMC links, MLflow run hierarchy, tags, metrics, and FINISHED status."""
    exp = xmc_entities_validator.validate_experiment(stub, xid, EXPECTED_TITLE_PATTERN)
    exp_id = _poll_mlflow_experiment_id(mlflow_uri, exp.title)
    parent, children = _poll_runs_status(mlflow_uri, exp_id, xid, expected_wus, "FINISHED")

    ui_base = (verifier_cli.get_flag(FLAGS, "mlflow_ui_base_url", "") or mlflow_uri).rstrip("/")
    _assert_url_chip(
        exp.urls,
        EXPERIMENT_URL_DISPLAY_NAME,
        f"{ui_base}/#/experiments/{exp_id}",
        f"Experiment {xid}",
    )
    _assert_url_chip(
        exp.urls, EXPERIMENT_URL_DISPLAY_NAME, "compareRunsMode=CHART", f"Experiment {xid}"
    )

    parent_id = parent["info"]["run_id"]
    _assert_run_tags(
        _tags_map(parent), ("xmc.experiment_id", "xmc.experiment_url"), xid, f"parent {parent_id}"
    )

    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_wus = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    by_wid = {_tags_map(c).get("xmc.work_unit_id", ""): c for c in children}
    for wu in raw_wus:
        wid = wu.name.split("/")[-1]
        child = by_wid.get(wid)
        if child is None:
            raise AssertionError(f"Missing MLflow child run for WorkUnit {wid}.")
        child_id = child["info"]["run_id"]
        ctags = _tags_map(child)
        if ctags.get("mlflow.parentRunId") != parent_id:
            raise AssertionError(f"Child run {child_id} has wrong mlflow.parentRunId.")
        _assert_run_tags(
            ctags,
            (
                "xmc.experiment_id",
                "xmc.work_unit_id",
                "xmc.execution_id",
                "xmc.experiment_url",
                "xmc.work_unit_url",
            ),
            xid,
            f"child {child_id}",
        )
        _assert_url_chip(
            wu.urls,
            WORK_UNIT_URL_DISPLAY_NAME,
            f"{ui_base}/#/experiments/{exp_id}/runs/{child_id}",
            f"WorkUnit {wid}",
        )
        metrics = {m["key"] for m in child.get("data", {}).get("metrics", []) if "key" in m}
        if not {"loss", "accuracy"}.issubset(metrics):
            raise AssertionError(
                f"Child run {child_id} missing expected metrics (got {sorted(metrics)})."
            )
    print(
        f"  [✓] Verified MLflow parent ({parent_id}) + {len(children)} child runs, links, tags, and FINISHED status."
    )


def _verify_stop_transitions_to_killed(stub: Any, source_xid: str, mlflow_uri: str) -> None:
    """Clones a sleeping WorkUnit into a new experiment, stops it, and asserts KILLED."""
    print("\n[Step 5] Verifying stop -> MLflow KILLED status...")
    src_req = xmc_entities_validator._make_req(
        "ListWorkUnitsRequest", parent=f"experiments/{source_xid}"
    )
    src_wus = list(getattr(stub.ListWorkUnits(src_req), "work_units", []))
    image_path = src_wus[0].executable.jobset.jobs[0].spec.docker_image
    queue_name = xmc_entities_validator.extract_queue_name(src_wus[0])

    stop_title = f"MLflow Stop Check {uuid.uuid4().hex[:8]}"
    exp = stub.CreateExperiment(
        xmc_entities_validator._make_req(
            "CreateExperimentRequest",
            experiment=xmc_entities_validator._make_req("Experiment", title=stop_title),
        )
    )
    stop_xid = exp.name.split("/")[-1]
    wu_proto = src_wus[0]
    wu_proto.ClearField("name")
    wu_proto.executable.jobset.jobs[0].spec.command[:] = [
        "python3",
        "/app/worker.py",
        "--steps=120",
        "--step_sleep_sec=1.0",
    ]
    wu_proto.executable.scheduling_constraints.queue_name = queue_name
    wu_proto.executable.jobset.jobs[0].spec.docker_image = image_path
    stub.CreateWorkUnit(
        xmc_entities_validator._make_req(
            "CreateWorkUnitRequest", parent=exp.name, work_unit=wu_proto
        )
    )
    exp_id = _poll_mlflow_experiment_id(mlflow_uri, stop_title)
    _poll_runs_status(mlflow_uri, exp_id, stop_xid, 1, "RUNNING", timeout_sec=120)
    xmc_entities_validator.stop_work_units(stub, stop_xid)
    _poll_runs_status(mlflow_uri, exp_id, stop_xid, 1, "KILLED", timeout_sec=120)
    print(
        f"  [✓] Verified stopped experiment {stop_xid} transitioned MLflow child and parent runs to KILLED."
    )


def _extra_mlflow_verification(stub: Any, xid: str) -> None:
    """Runs MLflow API assertions and optional stop -> KILLED verification."""
    mlflow_uri = verifier_cli.get_flag(FLAGS, "mlflow_uri", "").strip()
    if not mlflow_uri:
        raise ValueError("--mlflow_uri (or MLFLOW_TRACKING_URI) is required for mlflow_job_test.")
    expected_wus = verifier_cli.resolve_expected_work_units(DEFAULT_WORK_UNITS)
    _assert_completed_mlflow_state(stub, xid, expected_wus, mlflow_uri)
    if verifier_cli.get_flag(FLAGS, "verify_stop", True):
        _verify_stop_transitions_to_killed(stub, xid, mlflow_uri)


def main(argv: Sequence[str]) -> None:
    """Main verification routine for mlflow_job workload."""
    expected_wus = verifier_cli.resolve_expected_work_units(DEFAULT_WORK_UNITS)
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC MLflow Sweep Job Verification & Assertions",
        summary_title="ALL MLFLOW JOB ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_worker_names=["trainer"] * expected_wus,
        extra_step=_extra_mlflow_verification,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
