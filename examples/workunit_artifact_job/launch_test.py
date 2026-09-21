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

"""Unit tests for WorkUnit artifact verification in examples/workunit_artifact_job/launch.py."""

from __future__ import annotations

import sys
import types
import unittest
from types import SimpleNamespace
from unittest.mock import MagicMock


def _ensure_hermetic_dependencies() -> None:
    """Provides lightweight stub modules when run outside the XMC wheel venv."""
    try:
        import xmanager_cloud.experiment_state_server.proto.artifact_pb2  # noqa: F401

        return
    except ModuleNotFoundError:
        pass

    dotenv_mod = types.ModuleType("dotenv")
    dotenv_mod.load_dotenv = lambda *args, **kwargs: None
    sys.modules.setdefault("dotenv", dotenv_mod)

    absl_mod = types.ModuleType("absl")
    app_mod = types.ModuleType("absl.app")
    app_mod.UsageError = RuntimeError
    app_mod.run = lambda fn: None
    flags_mod = types.ModuleType("absl.flags")
    flags_mod.FLAGS = SimpleNamespace(experiment_title="Test", queue="cpu-queue")
    flags_mod.DEFINE_string = lambda *args, **kwargs: None
    absl_mod.app = app_mod
    absl_mod.flags = flags_mod
    sys.modules.setdefault("absl", absl_mod)
    sys.modules.setdefault("absl.app", app_mod)
    sys.modules.setdefault("absl.flags", flags_mod)

    xmanager_mod = types.ModuleType("xmanager")
    xm_cloud_mod = types.ModuleType("xmanager.xm_cloud")
    xm_cloud_mod.snapshot_code = SimpleNamespace(push_repo_state=None)
    xmanager_mod.xm_cloud = xm_cloud_mod
    sys.modules.setdefault("xmanager", xmanager_mod)
    sys.modules.setdefault("xmanager.xm_cloud", xm_cloud_mod)

    class _LifecyclePhase:
        INPUT = 1
        EXECUTION = 2
        OUTPUT = 3

        @staticmethod
        def Name(val: int) -> str:
            return {1: "INPUT", 2: "EXECUTION", 3: "OUTPUT"}.get(val, "UNKNOWN")

    class _ArtifactPayload(SimpleNamespace):
        def WhichOneof(self, _name: str) -> str:
            for field in ("code_source", "text", "url", "chart", "managed_xprof"):
                if getattr(self, field, None) is not None:
                    return field
            return ""

    class _Artifact(SimpleNamespace):
        LifecyclePhase = _LifecyclePhase

    artifact_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.artifact_pb2")
    artifact_pb2.Artifact = _Artifact
    artifact_pb2.ArtifactPayload = _ArtifactPayload
    artifact_pb2.CodeSourceArtifact = SimpleNamespace
    artifact_pb2.TextArtifact = SimpleNamespace
    artifact_pb2.UrlArtifact = SimpleNamespace

    api_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.api_pb2")
    api_pb2.GetWorkUnitRequest = SimpleNamespace
    api_pb2.GetArtifactRequest = SimpleNamespace
    api_pb2.ListArtifactsRequest = SimpleNamespace
    api_pb2.CreateWorkUnitRequest = SimpleNamespace
    api_pb2.CreateArtifactRequest = SimpleNamespace

    priority_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.priority_pb2")
    priority_pb2.Priority = SimpleNamespace(NORMAL=2)

    resources_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.resources_pb2")
    resources_pb2.ResourceSet = SimpleNamespace
    resources_pb2.CpuResources = SimpleNamespace

    url_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.url_pb2")
    url_pb2.Url = SimpleNamespace

    class _WorkUnit(SimpleNamespace):
        WorkUnitType = SimpleNamespace(STANDARD=1)

    work_unit_pb2 = types.ModuleType("xmanager_cloud.experiment_state_server.proto.work_unit_pb2")
    work_unit_pb2.WorkUnit = _WorkUnit
    work_unit_pb2.Executable = SimpleNamespace
    work_unit_pb2.SchedulingConstraints = SimpleNamespace
    work_unit_pb2.JobSetExecutable = SimpleNamespace
    work_unit_pb2.KubernetesJob = SimpleNamespace
    work_unit_pb2.ExecutableSpec = SimpleNamespace

    proto_pkg = types.ModuleType("xmanager_cloud.experiment_state_server.proto")
    proto_pkg.api_pb2 = api_pb2
    proto_pkg.artifact_pb2 = artifact_pb2
    proto_pkg.priority_pb2 = priority_pb2
    proto_pkg.resources_pb2 = resources_pb2
    proto_pkg.url_pb2 = url_pb2
    proto_pkg.work_unit_pb2 = work_unit_pb2

    xid_msg_pb2 = types.ModuleType("xmanager_cloud.xid_service.proto.messages_pb2")
    xid_msg_pb2.XidAcls = SimpleNamespace

    xid_proto_pkg = types.ModuleType("xmanager_cloud.xid_service.proto")
    xid_proto_pkg.messages_pb2 = xid_msg_pb2

    sys.modules.setdefault("xmanager_cloud", types.ModuleType("xmanager_cloud"))
    sys.modules.setdefault(
        "xmanager_cloud.experiment_state_server",
        types.ModuleType("xmanager_cloud.experiment_state_server"),
    )
    sys.modules.setdefault("xmanager_cloud.experiment_state_server.proto", proto_pkg)
    sys.modules.setdefault("xmanager_cloud.experiment_state_server.proto.api_pb2", api_pb2)
    sys.modules.setdefault(
        "xmanager_cloud.experiment_state_server.proto.artifact_pb2", artifact_pb2
    )
    sys.modules.setdefault(
        "xmanager_cloud.experiment_state_server.proto.priority_pb2", priority_pb2
    )
    sys.modules.setdefault(
        "xmanager_cloud.experiment_state_server.proto.resources_pb2", resources_pb2
    )
    sys.modules.setdefault("xmanager_cloud.experiment_state_server.proto.url_pb2", url_pb2)
    sys.modules.setdefault(
        "xmanager_cloud.experiment_state_server.proto.work_unit_pb2", work_unit_pb2
    )
    sys.modules.setdefault(
        "xmanager_cloud.xid_service", types.ModuleType("xmanager_cloud.xid_service")
    )
    sys.modules.setdefault("xmanager_cloud.xid_service.proto", xid_proto_pkg)
    sys.modules.setdefault("xmanager_cloud.xid_service.proto.messages_pb2", xid_msg_pb2)


_ensure_hermetic_dependencies()

from examples.workunit_artifact_job import launch  # noqa: E402


def _make_work_unit(
    name: str = "experiments/100/workUnits/1",
    seconds: int = 1700000000,
    nanos: int = 100,
) -> SimpleNamespace:
    """Creates a baseline WorkUnit with a populated create_time."""
    return SimpleNamespace(
        name=name,
        create_time=SimpleNamespace(seconds=seconds, nanos=nanos),
    )


def _make_artifact(
    name: str = "artifacts/1",
    parent: str = "experiments/100/workUnits/1",
    phase: int = 1,
    seconds: int = 1700000001,
    nanos: int = 200,
) -> SimpleNamespace:
    """Creates a baseline Artifact with parent and create_time populated."""
    artifact = launch.build_input_artifact(
        title="worker.py",
        code_block="print('ok')",
        code_url="https://example.com/worker.py",
        notes="test artifact",
    )
    artifact.name = name
    artifact.parent = parent
    artifact.lifecycle_phase = phase
    if getattr(artifact, "create_time", None) is None:
        artifact.create_time = SimpleNamespace(seconds=0, nanos=0)
    artifact.create_time.seconds = seconds
    artifact.create_time.nanos = nanos
    return artifact


class TestWorkUnitArtifactVerification(unittest.TestCase):
    """Tests parent WorkUnit retrieval and timestamp ordering verification."""

    def test_timestamp_key_returns_seconds_and_nanos(self) -> None:
        ts = SimpleNamespace(seconds=1700000005, nanos=42)
        self.assertEqual(launch.timestamp_key(ts), (1700000005, 42))

    def test_assert_artifact_parent_and_timestamp_accepts_valid_order(self) -> None:
        work_unit = _make_work_unit(seconds=1700000000, nanos=500)
        artifact_equal = _make_artifact(seconds=1700000000, nanos=500)
        artifact_later = _make_artifact(seconds=1700000001, nanos=0)

        launch.assert_artifact_parent_and_timestamp(work_unit, artifact_equal)
        launch.assert_artifact_parent_and_timestamp(work_unit, artifact_later)

    def test_assert_artifact_parent_and_timestamp_rejects_parent_mismatch(self) -> None:
        work_unit = _make_work_unit(name="experiments/100/workUnits/1")
        wrong_parent_artifact = _make_artifact(parent="experiments/100/workUnits/2")

        with self.assertRaisesRegex(RuntimeError, "parent mismatch"):
            launch.assert_artifact_parent_and_timestamp(work_unit, wrong_parent_artifact)

    def test_assert_artifact_parent_and_timestamp_rejects_earlier_seconds(self) -> None:
        work_unit = _make_work_unit(seconds=1700000010, nanos=0)
        earlier_artifact = _make_artifact(seconds=1700000009, nanos=999999999)

        with self.assertRaisesRegex(RuntimeError, "Timestamp ordering violation"):
            launch.assert_artifact_parent_and_timestamp(work_unit, earlier_artifact)

    def test_assert_artifact_parent_and_timestamp_rejects_earlier_nanos(self) -> None:
        work_unit = _make_work_unit(seconds=1700000010, nanos=500)
        earlier_artifact = _make_artifact(seconds=1700000010, nanos=499)

        with self.assertRaisesRegex(RuntimeError, "Timestamp ordering violation"):
            launch.assert_artifact_parent_and_timestamp(work_unit, earlier_artifact)

    def test_verify_artifacts_passes_for_valid_parent_and_timestamps(self) -> None:
        work_unit_name = "experiments/100/workUnits/1"
        work_unit = _make_work_unit(name=work_unit_name, seconds=1700000000, nanos=100)
        art1 = _make_artifact(
            name="artifacts/1",
            parent=work_unit_name,
            phase=1,
            seconds=1700000000,
            nanos=100,
        )
        art2 = _make_artifact(
            name="artifacts/2",
            parent=work_unit_name,
            phase=3,
            seconds=1700000002,
            nanos=0,
        )

        by_name = {art1.name: art1, art2.name: art2}
        stub = MagicMock()
        stub.get_work_unit.return_value = work_unit
        stub._stub.GetArtifact.side_effect = lambda req: by_name[req.name]
        stub.list_artifacts.return_value = SimpleNamespace(artifacts=[art1, art2])

        launch.verify_artifacts(stub, work_unit_name, [art1, art2])

        stub.get_work_unit.assert_called_once()
        stub.list_artifacts.assert_called_once()
        list_req = stub.list_artifacts.call_args[0][0]
        self.assertEqual(list_req.parent, work_unit_name)

    def test_verify_artifacts_raises_when_listed_artifact_has_invalid_timestamp(self) -> None:
        work_unit_name = "experiments/100/workUnits/1"
        work_unit = _make_work_unit(name=work_unit_name, seconds=1700000010, nanos=0)
        valid_fetched = _make_artifact(
            name="artifacts/1",
            parent=work_unit_name,
            seconds=1700000011,
            nanos=0,
        )
        invalid_listed = _make_artifact(
            name="artifacts/1",
            parent=work_unit_name,
            seconds=1700000009,
            nanos=0,
        )

        stub = MagicMock()
        stub.get_work_unit.return_value = work_unit
        stub._stub.GetArtifact.return_value = valid_fetched
        stub.list_artifacts.return_value = SimpleNamespace(artifacts=[invalid_listed])

        with self.assertRaisesRegex(RuntimeError, "Timestamp ordering violation"):
            launch.verify_artifacts(stub, work_unit_name, [valid_fetched])


if __name__ == "__main__":
    unittest.main()
