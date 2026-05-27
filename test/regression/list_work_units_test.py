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
import time

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

  def test_list_created_work_units(self):
    """Tests that list work units contains the created work units."""
    self.assertIsNotNone(
        self.examples, 'ExamplesLib was not initialized.'
    )
    experiment_name = test_utils.create_experiment_with_work_units(
        self.examples,
        title='List created work units',
        notes='Regression test',
        work_unit_count=2,
    )

    # Wait for work units to be available in ESS with a timeout.
    timeout = datetime.timedelta(seconds=10)
    start_time = datetime.datetime.now(datetime.timezone.utc)
    response = None
    while datetime.datetime.now(
        datetime.timezone.utc
    ) - start_time < timeout and (
        response is None or len(response.work_units) < 2
    ):
      time.sleep(1)
      response = self.examples.list_work_units(
          experiment_name=experiment_name, page_size=10, page_token=''
      )

    self.assertIsNotNone(response, 'list_work_units never returned a response.')
    expected_work_units = [
        work_unit_pb2.WorkUnit(
            name=f'{experiment_name}/workUnits/{i+1}',
            work_unit_type=work_unit_pb2.WorkUnit.WorkUnitType.STANDARD,
            priority=priority_pb2.Priority.HIGH,
        )
        for i in range(2)
    ]
    with self.subTest(name='Work units details are correct'):
      compare.assertProto2CountEqual(
          self,
          expected_work_units,
          response.work_units,
          number_matters=True,
          ignored_fields=[
              'create_time',
              'update_time',
              'urls',
              'executable',
              'current_work_unit_execution',
          ],
      )

if __name__ == '__main__':
  googletest.main()
