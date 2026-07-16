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
"""Unit tests for experiment_state_api module."""

import os
import unittest
from unittest import mock

import google.auth
from google.oauth2 import id_token
from longrunning import operations_pb2
from xmanager_cloud.experiment_state_server import experiment_state_api
from xmanager_cloud.experiment_state_server.proto import (
    api_pb2 as experiment_state_service_pb2,
    artifact_pb2,
    experiment_pb2,
    work_unit_pb2,
)


class ExperimentStateApiTest(unittest.TestCase):
    def setUp(self):
        super().setUp()
        self._mock_stub = mock.create_autospec(
            experiment_state_service_pb2.ExperimentStateServiceStub,
        )
        self._mock_stub.CreateExperiment.return_value = experiment_pb2.Experiment()
        self._mock_stub.CreateWorkUnit.return_value = operations_pb2.Operation()
        self._mock_stub.GetExperiment.return_value = experiment_pb2.Experiment()
        self._mock_stub.GetWorkUnit.return_value = work_unit_pb2.WorkUnit()
        self._mock_stub.UpdateExperimentLaunchState.return_value = (
            experiment_pb2.Experiment()
        )
        self._mock_stub.UpdateExperiment.return_value = experiment_pb2.Experiment()
        self._mock_stub.UpdateWorkUnit.return_value = work_unit_pb2.WorkUnit()
        self._mock_stub.CreateArtifact.return_value = artifact_pb2.Artifact()
        self._mock_stub.ListArtifacts.return_value = (
            experiment_state_service_pb2.ListArtifactsResponse()
        )
        self._mock_stub.SearchArtifacts.return_value = (
            experiment_state_service_pb2.ListArtifactsResponse()
        )
        self._mock_stub.DeleteArtifact.return_value = artifact_pb2.Artifact()
        self._mock_stub.UpdateArtifact.return_value = artifact_pb2.Artifact()
        self._mock_stub.ListStatusMessages.return_value = (
            experiment_state_service_pb2.ListStatusMessagesResponse()
        )
        self._mock_stub.SearchStatusMessages.return_value = (
            experiment_state_service_pb2.ListStatusMessagesResponse()
        )
        self._mock_channel = mock.MagicMock()
        self.enterContext(
            mock.patch.object(
                experiment_state_api,
                "_create_experiment_state_server_stub",
                return_value=(self._mock_stub, self._mock_channel),
            )
        )

    def tearDown(self):
        super().tearDown()
        experiment_state_api.get_experiment_state_api.cache_clear()

    def test_get_experiment_state_api(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        experiment_state_api._create_experiment_state_server_stub.assert_called_once_with(
            experiment_state_api._XMANAGER_ENDPOINT,
        )
        self.assertEqual(ess_api._stub, self._mock_stub)

    def test_get_experiment_state_api_with_custom_endpoint(self):
        with mock.patch.dict(os.environ, {"XMANAGER_ENDPOINT": "custom_endpoint"}):
            ess_api = experiment_state_api.get_experiment_state_api()
            experiment_state_api._create_experiment_state_server_stub.assert_called_once_with(
                "custom_endpoint",
            )
            self.assertEqual(ess_api._stub, self._mock_stub)

    def test_create_experiment(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.CreateExperimentRequest()
        ess_api.create_experiment(request)
        self._mock_stub.CreateExperiment.assert_called_once_with(request)

    def test_create_work_unit(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.CreateWorkUnitRequest()
        ess_api.create_work_unit(request)
        self._mock_stub.CreateWorkUnit.assert_called_once_with(request)

    def test_get_experiment(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.GetExperimentRequest()
        ess_api.get_experiment(request)
        self._mock_stub.GetExperiment.assert_called_once_with(request)

    def test_get_work_unit(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.GetWorkUnitRequest()
        ess_api.get_work_unit(request)
        self._mock_stub.GetWorkUnit.assert_called_once_with(request)

    def test_update_experiment_launch_state(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.UpdateExperimentLaunchStateRequest()
        ess_api.update_experiment_launch_state(request)
        self._mock_stub.UpdateExperimentLaunchState.assert_called_once_with(request)

    def test_update_experiment(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.UpdateExperimentRequest()
        ess_api.update_experiment(request)
        self._mock_stub.UpdateExperiment.assert_called_once_with(request)

    def test_update_work_unit(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.UpdateWorkUnitRequest()
        ess_api.update_work_unit(request)
        self._mock_stub.UpdateWorkUnit.assert_called_once_with(request)

    def test_create_artifact(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.CreateArtifactRequest()
        ess_api.create_artifact(request)
        self._mock_stub.CreateArtifact.assert_called_once_with(request)

    def test_list_artifacts(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.ListArtifactsRequest()
        ess_api.list_artifacts(request)
        self._mock_stub.ListArtifacts.assert_called_once_with(request)

    def test_search_artifacts(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.ListArtifactsRequest()
        ess_api.search_artifacts(request)
        self._mock_stub.SearchArtifacts.assert_called_once_with(request)

    def test_delete_artifact(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.DeleteArtifactRequest()
        ess_api.delete_artifact(request)
        self._mock_stub.DeleteArtifact.assert_called_once_with(request)

    def test_update_artifact(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.UpdateArtifactRequest()
        ess_api.update_artifact(request)
        self._mock_stub.UpdateArtifact.assert_called_once_with(request)

    def test_list_status_messages(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.ListStatusMessagesRequest()
        ess_api.list_status_messages(request)
        self._mock_stub.ListStatusMessages.assert_called_once_with(request)

    def test_search_status_messages(self):
        ess_api = experiment_state_api.get_experiment_state_api()
        request = experiment_state_service_pb2.ListStatusMessagesRequest()
        ess_api.search_status_messages(request)
        self._mock_stub.SearchStatusMessages.assert_called_once_with(request)


class GetCurrentUserEmailTest(unittest.TestCase):
    def setUp(self):
        super().setUp()
        self.mock_default = self.enterContext(
            mock.patch.object(google.auth, "default", autospec=True)
        )
        self.mock_creds = mock.MagicMock()
        self.mock_default.return_value = (self.mock_creds, None)
        self.mock_creds.valid = True
        del self.mock_creds.service_account_email
        self.mock_creds.id_token = None

        self.mock_verify_oauth2_token = self.enterContext(
            mock.patch.object(id_token, "verify_oauth2_token", autospec=True)
        )
        self.mock_authorized_session = self.enterContext(
            mock.patch(
                "google.auth.transport.requests.AuthorizedSession", autospec=True
            )
        )

    def test_service_account_email(self):
        self.mock_creds.service_account_email = "sa@example.com"
        self.assertEqual(
            experiment_state_api.get_current_user_email(), "sa@example.com"
        )

    def test_id_token_email(self):
        self.mock_creds.id_token = "some_token"
        self.mock_verify_oauth2_token.return_value = {"email": "id@example.com"}
        self.assertEqual(
            experiment_state_api.get_current_user_email(), "id@example.com"
        )

    def test_userinfo_email_after_id_token_value_error(self):
        self.mock_creds.id_token = "some_token"
        self.mock_verify_oauth2_token.side_effect = ValueError("Invalid token")
        mock_session = self.mock_authorized_session.return_value.__enter__.return_value
        mock_response = mock.MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {"email": "userinfo@example.com"}
        mock_session.get.return_value = mock_response

        self.assertEqual(
            experiment_state_api.get_current_user_email(), "userinfo@example.com"
        )
        mock_session.get.assert_called_once_with(
            "https://openidconnect.googleapis.com/v1/userinfo"
        )

    def test_userinfo_email_no_id_token(self):
        mock_session = self.mock_authorized_session.return_value.__enter__.return_value
        mock_response = mock.MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {"email": "userinfo@example.com"}
        mock_session.get.return_value = mock_response

        self.assertEqual(
            experiment_state_api.get_current_user_email(), "userinfo@example.com"
        )
        mock_session.get.assert_called_once_with(
            "https://openidconnect.googleapis.com/v1/userinfo"
        )

    def test_userinfo_not_200(self):
        mock_session = self.mock_authorized_session.return_value.__enter__.return_value
        mock_response = mock.MagicMock()
        mock_response.status_code = 403
        mock_session.get.return_value = mock_response

        with self.assertRaisesRegex(RuntimeError, "Failed to get current user email"):
            experiment_state_api.get_current_user_email()

    def test_userinfo_no_email(self):
        mock_session = self.mock_authorized_session.return_value.__enter__.return_value
        mock_response = mock.MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {}
        mock_session.get.return_value = mock_response

        with self.assertRaisesRegex(RuntimeError, "Failed to get current user email"):
            experiment_state_api.get_current_user_email()

    def test_auth_default_exception(self):
        self.mock_default.side_effect = Exception("Auth failed")
        with self.assertRaisesRegex(RuntimeError, "Failed to get current user email"):
            experiment_state_api.get_current_user_email()


if __name__ == "__main__":
    unittest.main()
