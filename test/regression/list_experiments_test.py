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
"""Regression tests for list_experiments API."""

import time

from absl.testing import absltest as googletest
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.experiment_state_server.proto import (
    experiment_pb2,
    priority_pb2,
    status_pb2,
)
from xmanager_cloud.tests.regression import test_utils
from xmanager_cloud.xid_service.proto import messages_pb2 as xid_messages_pb2


class ExperimentRegressionTest(googletest.TestCase):
    """Regression test suite for list_experiments API calls."""

    examples: examples_lib.ExamplesLib | None = None

    def setUp(self):
        """Sets up ExamplesLib instance before test execution."""
        super().setUp()
        self.examples = examples_lib.ExamplesLib()

    def tearDown(self):
        """Cleans up ExamplesLib connection after test execution."""
        super().tearDown()
        super().tearDown()
        if self.examples is not None:
            self.examples.close()

    def test_list_created_experiments(self):
        """Tests that list experiments contains the created experiment."""
        assert self.examples is not None, "ExamplesLib was not initialized."
        experiment_name = test_utils.create_experiment_with_work_units(
            self.examples, title="List created experiment", notes="Regression test"
        )
        experiment_xid = experiment_name.split("/")[-1]

        # Wait for experiment to be available in ESS with a timeout.
        timeout = 10
        start_time = time.time()
        response = None
        while time.time() - start_time < timeout and (
            response is None or len(response.experiments) == 0
        ):
            time.sleep(1)
            response = self.examples.list_experiments(
                page_size=1, page_token="", page_filter=f'xid="{experiment_xid}"'
            )

        self.assertIsNotNone(response, "list_experiments never returned a response.")
        self.assertLen(
            response.experiments, 1, "Experiment was not found in list_experiments."
        )
        [experiment] = response.experiments
        expected_experiment = experiment_pb2.Experiment(
            name=experiment_name,
            title="List created experiment",
            tags=["regression", "test"],
            notes="Regression test",
            acls=xid_messages_pb2.XidAcls(
                owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
            ),
        )
        with self.subTest(name="Response has one experiment"):
            self.assertLen(response.experiments, 1)
        with self.subTest(name="Experiment details are correct"):
            compare.assertProto2Contains(self, expected_experiment, experiment)
        with self.subTest(name="Experiment priority is valid"):
            self.assertNotEqual(experiment.priority, priority_pb2.PRIORITY_UNSPECIFIED)
            self.assertGreater(experiment.create_time.seconds, 0)
            self.assertGreater(experiment.update_time.seconds, 0)
        with self.subTest(name="Experiment status is completed"):
            compare.assertProto2Contains(
                self,
                experiment_pb2.Experiment(
                    status=status_pb2.ExperimentStatus(
                        launch_state=status_pb2.ExperimentStatus.LaunchState.COMPLETED
                    )
                ),
                experiment,
            )

    def test_list_nonexistent_experiment_returns_empty(self):
        """Tests that list experiments returns empty for a nonexistent experiment."""
        assert self.examples is not None, "ExamplesLib was not initialized."
        response = self.examples.list_experiments(
            page_size=1, page_token="", page_filter='xid="000000"'
        )
        with self.subTest(name="Response has no experiments"):
            self.assertEmpty(response.experiments)


if __name__ == "__main__":
    googletest.main()
