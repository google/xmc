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
"""Regression tests for list_artifacts API."""

import datetime
import time

from absl.testing import absltest as googletest
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.experiment_state_server.proto import artifact_pb2, url_pb2
from xmanager_cloud.tests.regression import test_utils

_TIMEOUT_SECONDS = 10
_EXPERIMENT_ARTIFACTS_COUNT = 3
_WORK_UNIT_ARTIFACTS_COUNT = 2
_WORK_UNIT_COUNT = 1


class ArtifactRegressionTest(googletest.TestCase):
    """Regression test suite for list_artifacts API calls."""

    examples: examples_lib.ExamplesLib | None = None

    def setUp(self):
        """Sets up ExamplesLib instance before test execution."""
        super().setUp()
        self.examples = examples_lib.ExamplesLib()

    def tearDown(self):
        """Cleans up ExamplesLib connection after test execution."""
        super().tearDown()
        if self.examples is not None:
            self.examples.close()

    def test_list_created_artifacts(self):
        """Tests that list artifacts contains the created artifacts."""
        self.assertIsNotNone(self.examples, "ExamplesLib was not initialized.")
        experiment_artifacts_count = _EXPERIMENT_ARTIFACTS_COUNT
        work_unit_artifacts_count = _WORK_UNIT_ARTIFACTS_COUNT
        work_unit_count = _WORK_UNIT_COUNT
        artifact_count = experiment_artifacts_count + (work_unit_count * work_unit_artifacts_count)
        experiment_name, _, _ = test_utils.create_experiment_with_artifacts(
            self.examples,
            title="List created artifacts",
            notes="Regression test",
            work_unit_count=work_unit_count,
            experiment_artifacts_count=experiment_artifacts_count,
            work_unit_artifacts_count=work_unit_artifacts_count,
        )

        # Wait for experiment artifacts to be available in ESS with a timeout.
        timeout = datetime.timedelta(seconds=_TIMEOUT_SECONDS)
        start_time = datetime.datetime.now(datetime.UTC)
        response = None
        while datetime.datetime.now(datetime.UTC) - start_time < timeout and (
            response is None or len(response.artifacts) < artifact_count
        ):
            response = self.examples.list_artifacts(
                parent_name=experiment_name,
                page_size=artifact_count + 1,
                page_token="",
            )
            time.sleep(1)

        if response is None:
            self.fail(
                f"No response from list_artifacts within {_TIMEOUT_SECONDS}s timeout;"
                " this may indicate a server error or a timeout."
            )
        self.assertLen(
            response.artifacts,
            artifact_count,
            "Wrong number of artifacts: list_artifacts returned"
            f" {len(response.artifacts)} artifacts, but {artifact_count} were"
            " expected.",
        )
        expected_experiment_artifacts = [
            artifact_pb2.Artifact(
                parent=experiment_name,
                data_type=artifact_pb2.Artifact.DataType.TEXT,
                notes=f"Experiment artifact {artifact_id}",
                lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
                url=url_pb2.Url(
                    url=f"http://example.com/exp/{artifact_id}",
                    display_name=f"Exp Artifact {artifact_id}",
                ),
            )
            for artifact_id in range(experiment_artifacts_count)
        ]
        expected_wu_artifacts = [
            artifact_pb2.Artifact(
                parent=f"{experiment_name}/workUnits/1",
                data_type=artifact_pb2.Artifact.URL,
                notes=f"Work Unit 1 artifact {artifact_id}",
                lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
                url=url_pb2.Url(
                    url=f"http://example.com/wu/1/art/{artifact_id}",
                    display_name=f"WU 1 Artifact {artifact_id}",
                ),
            )
            for artifact_id in range(work_unit_artifacts_count)
        ]
        expected_artifacts = expected_experiment_artifacts + expected_wu_artifacts
        compare.assertProto2CountEqual(
            self,
            expected_artifacts,
            response.artifacts,
            number_matters=True,
            ignored_fields=[
                "name",
                "create_time",
                "update_time",
                "acls",
            ],
        )


if __name__ == "__main__":
    googletest.main()
