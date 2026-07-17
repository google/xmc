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
"""Regression tests for get_experiment API."""

import re
import time

import grpc
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
    """Regression test suite for get_experiment API calls."""

    examples: examples_lib.ExamplesLib | None = None
    # A large offset added to the latest existing XID to ensure the generated XID
    # does not exist.
    _NONEXISTENT_XID_OFFSET = 100_000

    def setUp(self):
        """Sets up ExamplesLib instance before test execution."""
        super().setUp()
        self.examples = examples_lib.ExamplesLib()

    def tearDown(self):
        """Cleans up ExamplesLib connection after test execution."""
        super().tearDown()
        if self.examples is not None:
            self.examples.close()

    def test_get_created_experiment(self):
        """Tests that get experiment contains the created experiment."""
        assert self.examples is not None, "ExamplesLib was not initialized."
        experiment_name = test_utils.create_experiment_with_work_units(
            self.examples, title="Get created experiment", notes="Regression test"
        )

        # Wait for experiment to be available in ESS with a timeout.
        timeout = 10
        start_time = time.time()
        experiment = None
        while time.time() - start_time < timeout and experiment is None:
            try:
                time.sleep(1)
                experiment = self.examples.get_experiment(experiment_name)
            except grpc.RpcError as e:
                if hasattr(e, "code") and e.code() in (
                    grpc.StatusCode.UNKNOWN,
                    grpc.StatusCode.NOT_FOUND,
                ):
                    pass
                else:
                    raise e

        self.assertIsNotNone(experiment, "get_experiment never returned a response.")
        expected_experiment = experiment_pb2.Experiment(
            name=experiment_name,
            title="Get created experiment",
            tags=["regression", "test"],
            notes="Regression test",
            acls=xid_messages_pb2.XidAcls(
                owners=[xid_messages_pb2.XidAcls.Role(all_authenticated_users=True)]
            ),
        )
        with self.subTest(name="Experiment details are correct"):
            compare.assertProto2Contains(self, expected_experiment, experiment)
        with self.subTest(name="Experiment priority is valid"):
            self.assertNotEqual(experiment.priority, priority_pb2.PRIORITY_UNSPECIFIED)
        with self.subTest(name="Experiment create time is valid"):
            self.assertGreater(experiment.create_time.seconds, 0)
        with self.subTest(name="Experiment update time is valid"):
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

    def test_get_nonexistent_experiment_raises_error(self):
        """Tests that get experiment raises error for a nonexistent experiment."""
        assert self.examples is not None, "ExamplesLib was not initialized."
        response = self.examples.list_experiments(page_size=1, page_token="", order_by="xid desc")
        if len(response.experiments) > 0:
            latest_xid = int(response.experiments[0].name.split("/")[-1])
            nonexistent_xid = latest_xid + self._NONEXISTENT_XID_OFFSET
        else:
            nonexistent_xid = self._NONEXISTENT_XID_OFFSET
        nonexistent_experiment_name = f"experiments/{nonexistent_xid}"

        expected_regex = (
            f"XID lookup failed for XID {re.escape(str(nonexistent_xid))}[\\s\\S]*"
            f"XID {re.escape(str(nonexistent_xid))} not found"
        )
        with self.assertRaisesRegex(grpc.RpcError, expected_regex):
            self.examples.get_experiment(experiment_name=nonexistent_experiment_name)


if __name__ == "__main__":
    googletest.main()
