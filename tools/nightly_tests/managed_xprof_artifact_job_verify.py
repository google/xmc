#!/usr/bin/env python3
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

"""Validation script for ManagedXprof artifact workload in XMC.

Asserts that the ManagedXprof WorkUnit Artifact experiment and its WorkUnit were
correctly created, scheduled to the expected queue, configured with ManagedXprof
artifact payloads attached during execution lifecycle, and executed to completion.

Usage:
    python3 -m tools.nightly_tests.managed_xprof_artifact_job_verify --xid=25
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

EXPECTED_TITLE_PATTERN: str = "ManagedXprof WorkUnit Artifact Example"
EXPECTED_WORK_UNITS: int = 1
DEFAULT_QUEUE: str = "cpu-queue"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("programmatic-xprof-worker",)
EXPECTED_ARTIFACT_TYPES: tuple[str, ...] = ("managed_xprof",)
DEFAULT_TIMEOUT_SEC: int = 600


def _assert_experiment(stub: Any, xid: str) -> Any:
    """Asserts experiment existence and title pattern."""
    print(f"\n[1/4] Asserting Experiment metadata for XID {xid}...")
    experiment = xmc_entities_validator.validate_experiment(
        stub=stub,
        xid=xid,
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
    )
    print(f"  [✓] Experiment Name   : {experiment.name}")
    print(f"  [✓] Experiment Title  : {experiment.title}")
    return experiment


def _assert_work_units(
    stub: Any, xid: str, expected_queue: str, timeout_sec: int
) -> list[dict[str, Any]]:
    """Asserts work unit execution state and queue mapping."""
    print(f"\n[2/4] Asserting {EXPECTED_WORK_UNITS} WorkUnit targeted at '{expected_queue}'...")
    wu_results = xmc_entities_validator.validate_work_units(
        stub=stub,
        xid=xid,
        expected_count=EXPECTED_WORK_UNITS,
        expected_queue=expected_queue,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        timeout_sec=timeout_sec,
    )
    for wu in wu_results:
        print(
            f"  [✓] WorkUnit #{wu['id']}: Worker='{wu['worker_name']}', Queue='{wu['queue']}', State='{wu['state']}'"
        )
    return wu_results


def _assert_jobset(stub: Any, xid: str) -> Any:
    """Validates JobSet structure."""
    print("\n[3/4] Validating JobSet structure...")
    req = xmc_entities_validator._make_req("ListWorkUnitsRequest", parent=f"experiments/{xid}")
    raw_work_units = list(getattr(stub.ListWorkUnits(req), "work_units", []))
    if not raw_work_units:
        raise AssertionError(f"No WorkUnits found for experiment {xid}.")
    xmc_entities_validator.validate_jobsets(
        work_unit=raw_work_units[0],
        expected_jobs=[
            {
                "name": "programmatic-xprof-worker",
                "replicas": 1,
                "spec": "programmatic-xprof-worker",
            }
        ],
    )
    return raw_work_units[0]


def _assert_artifacts(
    stub: Any, xid: str, wid: str, raw_wu_name: str, timeout_sec: int
) -> list[dict[str, Any]]:
    """Asserts ManagedXprofArtifact payloads."""
    print(f"\n[4/4] Validating WorkUnit Artifacts for {raw_wu_name} (WID: {wid})...")
    artifact_results = xmc_entities_validator.validate_artifacts(
        stub=stub,
        xid=xid,
        wid=wid,
        expected_artifact_types=EXPECTED_ARTIFACT_TYPES,
        timeout_sec=min(60, timeout_sec),
    )
    for art in artifact_results:
        print(f"  [✓] Artifact: Name='{art['name']}', Type='{art['type']}', Title='{art['title']}'")
    return artifact_results


def _print_banner(xid: str, expected_queue: str, timeout_sec: int) -> None:
    """Prints verification header banner."""
    common.print_banner(
        "XMC ManagedXprof Artifact Job Verification & Assertions",
        [
            ("Experiment XID", xid),
            ("Expected Title Pattern", f"'{EXPECTED_TITLE_PATTERN}'"),
            ("Expected WorkUnits Count", EXPECTED_WORK_UNITS),
            ("Expected Target Queue", expected_queue),
            ("Expected Worker Names", list(EXPECTED_WORKER_NAMES)),
            ("Expected Artifact Types", list(EXPECTED_ARTIFACT_TYPES)),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_summary(
    experiment: Any,
    wu_count: int,
    expected_queue: str,
    art_results: list[dict[str, Any]],
    ui_base_url: str,
    xid: str,
) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL MANAGED XPROF ARTIFACT ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Experiment", f"{experiment.name} (Title: '{experiment.title}')"),
            ("Verified WorkUnits", f"{wu_count} / {EXPECTED_WORK_UNITS}"),
            ("Target Queue", expected_queue),
            ("Verified Artifacts", f"{len(art_results)} ({list(EXPECTED_ARTIFACT_TYPES)})"),
            ("Web UI Link", f"{ui_base_url}/experiments/{xid}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for managed_xprof_artifact_job workload."""
    common.validate_argv(argv)
    xid = common.parse_xid(getattr(FLAGS, "xid", None))
    expected_queue = common.get_flag(FLAGS, "expected_queue", DEFAULT_QUEUE)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    ui_base_url = common.resolve_ui_base_url()

    _print_banner(xid, expected_queue, timeout_sec)
    with (
        common.handle_verification_errors(),
        common.ess_channel_context(getattr(FLAGS, "endpoint", None)) as (stub, _),
    ):
        experiment = _assert_experiment(stub, xid)
        wu_results = _assert_work_units(stub, xid, expected_queue, timeout_sec)
        raw_wu = _assert_jobset(stub, xid)
        art_results = _assert_artifacts(stub, xid, wu_results[0]["id"], raw_wu.name, timeout_sec)
        _print_summary(experiment, len(wu_results), expected_queue, art_results, ui_base_url, xid)


if __name__ == "__main__":
    common.run_main(main)
