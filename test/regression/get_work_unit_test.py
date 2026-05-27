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
import datetime
import re
import time

import grpc

from absl.testing import absltest as googletest
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.experiment_state_server.proto import priority_pb2
from xmanager_cloud.experiment_state_server.proto import work_unit_pb2
from xmanager_cloud.tests.regression import test_utils


class WorkUnitRegressionTest(googletest.TestCase):
  examples: examples_lib.ExamplesLib | None = None

  def setUp(self):
    super().setUp()
    self.examples = examples_lib.ExamplesLib()

  def tearDown(self):
    super().tearDown()
    if self.examples is not None:
      self.examples.close()

  def test_get_created_work_unit(self):
    """Tests that get work unit contains the created work unit."""
    self.assertIsNotNone(self.examples, 'ExamplesLib was not initialized.')
    experiment_name = test_utils.create_experiment_with_work_units(
        self.examples,
        title='Get created work unit',
        notes='Regression test',
        work_unit_count=1,
    )
    work_unit_name = f'{experiment_name}/workUnits/1'

    # Wait for work unit to be available in ESS with a timeout.
    timeout = datetime.timedelta(seconds=10)
    start_time = datetime.datetime.now(datetime.timezone.utc)
    work_unit = None
    while datetime.datetime.now(
        datetime.timezone.utc
    ) - start_time < timeout and work_unit is None:
      try:
        work_unit = self.examples.get_work_unit(work_unit_name)

      except grpc.RpcError as e:
        is_retryable_error = hasattr(e, 'code') and e.code() in (
            grpc.StatusCode.UNKNOWN,
            grpc.StatusCode.NOT_FOUND,
        )
        if not is_retryable_error:
          raise e
        # Wait for 1 second before retrying.
        time.sleep(1)

    self.assertIsNotNone(
        work_unit,
        f'get_work_unit never returned a response for {work_unit_name}.',
    )
    expected_work_unit = work_unit_pb2.WorkUnit(
        name=work_unit_name,
        work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
        priority=priority_pb2.Priority.HIGH,
    )
    with self.subTest(name='Work unit details are correct'):
      compare.assertProto2Contains(self, expected_work_unit, work_unit)
    with self.subTest(name='Work unit create time is positive'):
      self.assertGreater(work_unit.create_time.seconds, 0)
    with self.subTest(name='Work unit update time is positive'):
      self.assertGreater(work_unit.update_time.seconds, 0)

  def test_get_nonexistent_work_unit_raises_error(self):
    """Tests that get work unit raises error for a nonexistent work unit."""
    self.assertIsNotNone(self.examples, 'ExamplesLib was not initialized.')
    experiment_name = test_utils.create_experiment_with_work_units(
        self.examples,
        title='Get non-existent work unit',
        notes='Regression test',
        work_unit_count=1,
    )
    nonexistent_work_unit_name = f'{experiment_name}/workUnits/1000'

    xid = experiment_name.split('/')[-1]
    expected_regex = (
        f'GetWorkUnit with ID {re.escape(nonexistent_work_unit_name)} not found'
        f'[\\s\\S]*WorkUnit 1000 for XID {re.escape(xid)} not found'
    )
    with self.assertRaisesRegex(grpc.RpcError, expected_regex):
      self.examples.get_work_unit(work_unit_name=nonexistent_work_unit_name)


if __name__ == '__main__':
  googletest.main()
