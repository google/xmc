# Copyright 2026 Google LLC
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

"""Unit tests for distributed ML rank harvesting and replica detection heuristics."""

import unittest
from unittest.mock import MagicMock, patch

from xmc.client import Client
from xmc.dist_harvest import (
    GetReplicaIDAndSilenceStatus,
    get_replica_id_and_silence_status,
    harvest_client_metadata,
)


class TestHarvestClientMetadata(unittest.TestCase):
    """Test suite for harvest_client_metadata function and fallback hierarchy."""

    def test_explicit_parameters(self) -> None:
        xid, wu_id, exec_id, rep_id = harvest_client_metadata(
            xid=1001,
            work_unit_id=2002,
            execution_id=3003,
            replica_id="replica-1",
        )
        self.assertEqual(xid, "1001")
        self.assertEqual(wu_id, "2002")
        self.assertEqual(exec_id, "3003")
        self.assertEqual(rep_id, "replica-1")

    def test_fallback_client_public_attributes(self) -> None:
        mock_client = MagicMock()
        mock_client.xid = 5005
        mock_client.work_unit_id = 6006
        mock_client.execution_id = 7007
        mock_client.replica_id = "pub-replica"

        xid, wu_id, exec_id, rep_id = harvest_client_metadata(fallback_client=mock_client)
        self.assertEqual(xid, "5005")
        self.assertEqual(wu_id, "6006")
        self.assertEqual(exec_id, "7007")
        self.assertEqual(rep_id, "pub-replica")

    def test_fallback_client_private_attributes_fallback(self) -> None:
        class PrivateOnlyClient:
            _xid = 8008
            _work_unit_id = 9009
            _execution_id = 1010
            _replica_id = "priv-replica"
            xid = None
            work_unit_id = None
            execution_id = None
            replica_id = None

        client = PrivateOnlyClient()
        xid, wu_id, exec_id, rep_id = harvest_client_metadata(fallback_client=client)
        self.assertEqual(xid, "8008")
        self.assertEqual(wu_id, "9009")
        self.assertEqual(exec_id, "1010")
        self.assertEqual(rep_id, "priv-replica")

    def test_environment_variables(self) -> None:
        env = {
            "XMC_XID": "1111",
            "XMC_WORK_UNIT_ID": "2222",
            "XMC_EXECUTION_ID": "3333",
            "XMC_REPLICA_ID": "env-replica",
        }
        xid, wu_id, exec_id, rep_id = harvest_client_metadata(env=env)
        self.assertEqual(xid, "1111")
        self.assertEqual(wu_id, "2222")
        self.assertEqual(exec_id, "3333")
        self.assertEqual(rep_id, "env-replica")

    def test_defaults_unorchestrated(self) -> None:
        xid, wu_id, exec_id, rep_id = harvest_client_metadata(env={})
        self.assertEqual(xid, "0")
        self.assertEqual(wu_id, "0")
        self.assertEqual(exec_id, "0")
        self.assertEqual(rep_id, "0")


if __name__ == "__main__":
    unittest.main()


class TestDistHarvest(unittest.TestCase):
    """Test suite for get_replica_id_and_silence_status and 3-stage sequence."""

    def test_alias_equivalence(self) -> None:
        self.assertIs(GetReplicaIDAndSilenceStatus, get_replica_id_and_silence_status)

    # --- Stage 1: Strict Framework Ranks ---

    def test_stage1_pytorch_rank_zero(self) -> None:
        env = {"RANK": "0", "WORLD_SIZE": "4"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    def test_stage1_pytorch_rank_zero_leading_zeros(self) -> None:
        env = {"RANK": "00", "WORLD_SIZE": "4"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "00")
        self.assertFalse(should_silence)

    def test_stage1_pytorch_rank_nonzero(self) -> None:
        env = {"RANK": "1", "WORLD_SIZE": "4"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "1")
        self.assertTrue(should_silence)

    def test_stage1_jax_process_index(self) -> None:
        env = {"JAX_PROCESS_INDEX": "2"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "2")
        self.assertTrue(should_silence)

    def test_stage1_mpi_world_rank(self) -> None:
        env = {"OMPI_COMM_WORLD_RANK": "0"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    def test_stage1_priority_over_stage3(self) -> None:
        # Framework rank (process-level) must take precedence over pod-level job index
        env = {"RANK": "0", "JOB_COMPLETION_INDEX": "3"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    def test_whitespace_env_vars_ignored(self) -> None:
        # Whitespace values in env vars should be ignored and fall through to default
        env = {"RANK": "   ", "WORLD_SIZE": "   ", "JOB_COMPLETION_INDEX": "   "}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    # --- Stage 2: Fail-Closed Safety Net ---

    @patch("xmc.dist_harvest.logger.warning")
    def test_stage2_world_size_greater_than_one(self, mock_warning: MagicMock) -> None:
        env = {"WORLD_SIZE": "4"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "-1")
        self.assertTrue(should_silence)
        mock_warning.assert_called_once()

    def test_stage2_world_size_equal_one_does_not_trigger(self) -> None:
        # Single process world size 1 should fall through to Stage 3 default
        env = {"WORLD_SIZE": "1"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    def test_stage2_ray_address(self) -> None:
        env = {"RAY_ADDRESS": "127.0.0.1:6379"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "-1")
        self.assertTrue(should_silence)

    def test_stage2_master_addr(self) -> None:
        env = {"MASTER_ADDR": "10.0.0.1"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "-1")
        self.assertTrue(should_silence)

    # --- Stage 3: Orchestrator / Single-Node Fallback ---

    def test_stage3_xmc_job_index(self) -> None:
        env = {"XMC_JOB_INDEX": "2"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "2")
        self.assertTrue(should_silence)

    def test_stage3_job_completion_index(self) -> None:
        env = {"JOB_COMPLETION_INDEX": "0"}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    def test_stage3_default_single_node(self) -> None:
        env = {}
        replica_id, should_silence = get_replica_id_and_silence_status(env)
        self.assertEqual(replica_id, "0")
        self.assertFalse(should_silence)

    # --- Multi-Replica Opt-In (log_all_replicas=True) ---

    def test_log_all_replicas_bypasses_silencer_stage1(self) -> None:
        env = {"RANK": "1"}
        replica_id, should_silence = get_replica_id_and_silence_status(env, log_all_replicas=True)
        self.assertEqual(replica_id, "1")
        self.assertFalse(should_silence)

    def test_log_all_replicas_bypasses_silencer_stage2(self) -> None:
        env = {"WORLD_SIZE": "8"}
        replica_id, should_silence = get_replica_id_and_silence_status(env, log_all_replicas=True)
        self.assertEqual(replica_id, "-1")
        self.assertFalse(should_silence)

    def test_log_all_replicas_bypasses_silencer_stage3(self) -> None:
        env = {"JOB_COMPLETION_INDEX": "4"}
        replica_id, should_silence = get_replica_id_and_silence_status(env, log_all_replicas=True)
        self.assertEqual(replica_id, "4")
        self.assertFalse(should_silence)

    # --- Client Integration Tests ---

    def test_client_integration_silenced(self) -> None:
        env = {"RANK": "2"}
        client = Client(base_url="https://xmc.example.com", env=env)
        self.assertEqual(client.replica_id, "2")
        self.assertTrue(client.is_silenced)

        # POST should return {} without network request when silenced
        res = client.post("v1/metrics", data={"loss": 0.1})
        self.assertEqual(res, {})

    @patch("urllib.request.urlopen")
    def test_client_integration_opt_in_unsilenced(self, mock_urlopen: MagicMock) -> None:
        mock_response = MagicMock()
        mock_response.read.return_value = b'{"status": "ok"}'
        mock_response.__enter__.return_value = mock_response
        mock_urlopen.return_value = mock_response

        env = {"RANK": "2"}
        client = Client(base_url="https://xmc.example.com", env=env, log_all_replicas=True)
        self.assertEqual(client.replica_id, "2")
        self.assertFalse(client.is_silenced)

        res = client.post("v1/metrics", data={"loss": 0.1})
        self.assertEqual(res, {"status": "ok"})
        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.headers.get("X-replica-id"), "2")


if __name__ == "__main__":
    unittest.main()
