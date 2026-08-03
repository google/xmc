# Copyright 2025 Google LLC
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
"""Regression test for Diagon ManagedXprof work-unit-level artifacts."""

import datetime
import time

import grpc
from absl.testing import absltest as googletest, parameterized
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.tests.regression import test_utils

_TIMEOUT_SECONDS = 15
_RETRY_DELAY_SECONDS = 1


class ManagedXprofArtifactRegressionTest(parameterized.TestCase):
    """Regression test suite for work-unit-level ManagedXprof artifacts."""

    examples: examples_lib.ExamplesLib | None = None

    def setUp(self) -> None:
        """Sets up ExamplesLib instance before test execution."""
        super().setUp()
        self.examples = examples_lib.ExamplesLib()

    def tearDown(self) -> None:
        """Closes the ExamplesLib instance after test execution."""
        super().tearDown()
        if self.examples is not None:
            self.examples.close()

    def test_managed_xprof_artifact_round_trip_at_work_unit(self) -> None:
        """Tests attaching a ManagedXprof artifact to each of two work units."""
        self.assertIsNotNone(self.examples, "ExamplesLib was not initialized.")

        # 1. Create an experiment with two work units so the work-unit-scoped
        #    artifact path is exercised for more than a single unit.
        experiment_name = test_utils.create_experiment_with_work_units(
            self.examples,
            title="Diagon ManagedXprof Smoke Test",
            notes="Verifies ManagedXprof artifact readback at the work-unit level",
            work_unit_count=2,
        )

        # 2. Each work unit owns its artifact, so attach and verify one per unit.
        for work_unit_id in (1, 2):
            self._attach_and_verify_managed_xprof_artifact(experiment_name, work_unit_id)

    def _attach_and_verify_managed_xprof_artifact(
        self, experiment_name: str, work_unit_id: int
    ) -> None:
        """Attaches and verifies a ManagedXprof artifact for one work unit."""
        work_unit_name = f"{experiment_name}/workUnits/{work_unit_id}"

        display_name = "smoke-diagon-display-name"
        run_group = "smoke-diagon-run-group"

        # Attach a ManagedXprof artifact to the work unit (not the experiment).
        artifact = test_utils.prepare_managed_xprof_artifact(
            display_name=display_name,
            run_group=run_group,
        )
        create_art_resp = self.examples.create_artifact(
            parent_name=work_unit_name,
            artifact=artifact,
        )
        artifact_name = create_art_resp.name

        # Read back the artifact and verify its payload fields.
        timeout = datetime.timedelta(seconds=_TIMEOUT_SECONDS)
        start_time = datetime.datetime.now(datetime.timezone.utc)
        got_artifact = None
        while datetime.datetime.now(datetime.timezone.utc) - start_time < timeout and (
            got_artifact is None or not got_artifact.payload.HasField("managed_xprof")
        ):
            try:
                got_artifact = self.examples.get_artifact(artifact_name)
            except grpc.RpcError as e:
                if hasattr(e, "code") and e.code() in (
                    grpc.StatusCode.UNKNOWN,
                    grpc.StatusCode.NOT_FOUND,
                ):
                    time.sleep(_RETRY_DELAY_SECONDS)
                    continue
                raise
            time.sleep(_RETRY_DELAY_SECONDS)

        self.assertIsNotNone(
            got_artifact,
            f"get_artifact never returned a response for {artifact_name}.",
        )
        self.assertTrue(
            got_artifact.payload.HasField("managed_xprof"),
            "Expected payload.managed_xprof to be populated on readback. Got"
            f" artifact:\n{got_artifact}",
        )

        self.assertEqual(
            got_artifact.payload.managed_xprof.display_name,
            display_name,
        )
        self.assertEqual(
            got_artifact.payload.managed_xprof.run_group,
            run_group,
        )


if __name__ == "__main__":
    googletest.main()
