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
from typing import Callable
import grpc

from absl.testing import parameterized
from xmanager_cloud.examples import examples_lib
from xmanager_cloud.experiment_state_server.proto import artifact_pb2
from xmanager_cloud.experiment_state_server.proto import url_pb2
from xmanager_cloud.tests.regression import test_utils


_TIMEOUT_SECONDS = 10
_RETRY_DELAY_SECONDS = 1
_EXPERIMENT_ARTIFACTS_COUNT = 1
_WORK_UNIT_ARTIFACTS_COUNT = 1
_WORK_UNIT_COUNT = 1
_NONEXISTENT_ARTIFACT_OFFSET = 1021


class ArtifactRegressionTest(parameterized.TestCase):
  examples: examples_lib.ExamplesLib | None = None

  def setUp(self) -> None:
    super().setUp()
    self.examples = examples_lib.ExamplesLib()

  def tearDown(self) -> None:
    super().tearDown()
    if self.examples is not None:
      self.examples.close()

  @parameterized.named_parameters(
      dict(
          testcase_name='experiment_artifact',
          title='Get experiment artifact',
          work_unit_count=_WORK_UNIT_COUNT,
          get_artifact_name_fn=(
              lambda experiment_artifact_names, _: experiment_artifact_names[0]
          ),
          expected_parent_fn=lambda exp_name: exp_name,
          expected_data_type=artifact_pb2.Artifact.DataType.TEXT,
          expected_notes='Experiment artifact 0',
          expected_lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.EXECUTION,
          expected_url='http://example.com/exp/0',
          expected_display_name='Exp Artifact 0',
      ),
      dict(
          testcase_name='work_unit_artifact',
          title='Get work unit artifact',
          work_unit_count=_WORK_UNIT_COUNT,
          get_artifact_name_fn=(
              lambda _, work_unit_artifact_names: work_unit_artifact_names[0]
          ),
          expected_parent_fn=lambda exp_name: f'{exp_name}/workUnits/1',
          expected_data_type=artifact_pb2.Artifact.DataType.URL,
          expected_notes='Work Unit 1 artifact 0',
          expected_lifecycle_phase=artifact_pb2.Artifact.LifecyclePhase.OUTPUT,
          expected_url='http://example.com/wu/1/art/0',
          expected_display_name='WU 1 Artifact 0',
      ),
  )
  def test_get_artifact(
      self,
      *,
      title: str,
      work_unit_count: int,
      get_artifact_name_fn: Callable[[list[str], list[str]], str],
      expected_parent_fn: Callable[[str], str],
      expected_data_type: artifact_pb2.Artifact.DataType,
      expected_notes: str,
      expected_lifecycle_phase: artifact_pb2.Artifact.LifecyclePhase,
      expected_url: str,
      expected_display_name: str,
  ) -> None:
    """Tests that get artifact contains the created artifact."""
    self.assertIsNotNone(self.examples, 'ExamplesLib was not initialized.')
    experiment_name, experiment_artifact_names, work_unit_artifact_names = (
        test_utils.create_experiment_with_artifacts(
            self.examples,
            title=title,
            notes='Regression test',
            work_unit_count=work_unit_count,
            experiment_artifacts_count=_EXPERIMENT_ARTIFACTS_COUNT,
            work_unit_artifacts_count=_WORK_UNIT_ARTIFACTS_COUNT,
        )
    )

    artifact_name = get_artifact_name_fn(
        experiment_artifact_names, work_unit_artifact_names
    )

    # Wait for experiment artifacts to be available in ESS with a timeout.
    timeout = datetime.timedelta(seconds=_TIMEOUT_SECONDS)
    start_time = datetime.datetime.now(datetime.timezone.utc)
    artifact = None
    retryable_errors = 0
    while datetime.datetime.now(
        datetime.timezone.utc
    ) - start_time < timeout and artifact is None:
      try:
        artifact = self.examples.get_artifact(artifact_name)
      except grpc.RpcError as e:
        if hasattr(e, 'code') and e.code() in (
            grpc.StatusCode.UNKNOWN,
            grpc.StatusCode.NOT_FOUND):
          retryable_errors += 1
          pass
        else:
          raise
      time.sleep(_RETRY_DELAY_SECONDS)

    if artifact is None and retryable_errors == 0:
      self.fail(
          f'No response from get_artifact within {_TIMEOUT_SECONDS}s timeout;'
          ' this may indicate a server error or a timeout.'
      )
    expected_artifact = artifact_pb2.Artifact(
        name=artifact_name,
        parent=expected_parent_fn(experiment_name),
        data_type=expected_data_type,
        notes=expected_notes,
        lifecycle_phase=expected_lifecycle_phase,
        url=url_pb2.Url(
            url=expected_url,
            display_name=expected_display_name,
        ),
    )
    self.assertIsNotNone(
        artifact,
        f'get_artifact never returned a response for {artifact_name}.',
    )
    with self.subTest(name='Artifact details are correct'):
      compare.assertProto2Contains(self, expected_artifact, artifact)
    with self.subTest(name='Artifact create time is positive'):
      self.assertGreater(artifact.create_time.seconds, 0)
    with self.subTest(name='Artifact update time is positive'):
      self.assertGreater(artifact.update_time.seconds, 0)

  def test_get_nonexistent_artifact_raises_error(self):
    """Tests that get artifact raises error for a nonexistent artifact."""
    self.assertIsNotNone(self.examples, 'ExamplesLib was not initialized.')
    _, experiment_artifact_names, _ = (
        test_utils.create_experiment_with_artifacts(
            self.examples,
            title='Get non-existent artifact',
            notes='Regression test',
            work_unit_count=_WORK_UNIT_COUNT,
            experiment_artifacts_count=_EXPERIMENT_ARTIFACTS_COUNT,
            work_unit_artifacts_count=_WORK_UNIT_ARTIFACTS_COUNT,
        )
    )
    artifact_id = int(experiment_artifact_names[0].split('/')[-1])
    nonexistent_artifact_id = artifact_id + _NONEXISTENT_ARTIFACT_OFFSET
    nonexistent_artifact_name = (
        f'artifacts/{nonexistent_artifact_id}'
    )

    expected_regex = (
        f'XID lookup failed for XID {re.escape(str(nonexistent_artifact_id))}'
        f'[\\s\\S]*XID {re.escape(str(nonexistent_artifact_id))} not found'
    )
    with self.assertRaisesRegex(grpc.RpcError, expected_regex):
      self.examples.get_artifact(artifact_name=nonexistent_artifact_name)


if __name__ == '__main__':
  googletest.main()
