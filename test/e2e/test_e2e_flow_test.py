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

"""Unit tests for verify_cms_bridge_reconciliation in test_e2e_flow."""

from __future__ import annotations

import json
import sys
import unittest
from unittest import mock

# Stub playwright before importing test_e2e_flow in the hermetic Bazel test sandbox.
sys.modules.setdefault("playwright", mock.MagicMock())
sys.modules.setdefault("playwright.sync_api", mock.MagicMock())

from test.e2e import test_e2e_flow  # noqa: E402


class VerifyCMSBridgeReconciliationTest(unittest.TestCase):
    """Tests for verify_cms_bridge_reconciliation and CRD CEL compliance."""

    @mock.patch.dict("os.environ", {"KUBE_CONTEXT": ""}, clear=False)
    @mock.patch("test.e2e.test_e2e_flow.subprocess.run")
    def test_noop_when_kube_context_empty(self, mock_run: mock.MagicMock) -> None:
        """verify_cms_bridge_reconciliation returns early when KUBE_CONTEXT is empty."""
        test_e2e_flow.verify_cms_bridge_reconciliation()
        mock_run.assert_not_called()

    @mock.patch.dict("os.environ", {"KUBE_CONTEXT": "kind-xmc-e2e"}, clear=False)
    @mock.patch("test.e2e.test_e2e_flow.time.sleep")
    @mock.patch("test.e2e.test_e2e_flow.subprocess.run")
    def test_applies_cel_valid_manifest_and_cleans_up(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
    ) -> None:
        """Manifest satisfies CRD CEL validation and cleans up after reaching DISABLED state."""
        mock_run.side_effect = [
            mock.MagicMock(stdout=""),
            mock.MagicMock(stdout="status:\n  state: COMPONENT_STATE_PENDING_RECONCILIATION\n"),
            mock.MagicMock(stdout="status:\n  state: COMPONENT_STATE_DISABLED\n"),
            mock.MagicMock(stdout=""),
        ]

        test_e2e_flow.verify_cms_bridge_reconciliation()

        self.assertEqual(mock_run.call_count, 4)
        mock_sleep.assert_called_once_with(1.0)
        spec = json.loads(mock_run.call_args_list[0].kwargs["input"])["spec"]
        self.assertEqual(spec["componentType"], "COMPONENT_TYPE_CUSTOM_3P_WRAPPER")
        self.assertEqual(spec["parameters"], {"authCheck": "true"})
        self.assertIn("delete", mock_run.call_args_list[3].args[0])

    @mock.patch.dict("os.environ", {"KUBE_CONTEXT": "kind-xmc-e2e"}, clear=False)
    @mock.patch("test.e2e.test_e2e_flow.time.time", side_effect=[100.0, 100.5, 131.0])
    @mock.patch("test.e2e.test_e2e_flow.time.sleep")
    @mock.patch("test.e2e.test_e2e_flow.subprocess.run")
    def test_raises_on_timeout_and_still_deletes_component(
        self,
        mock_run: mock.MagicMock,
        mock_sleep: mock.MagicMock,
        _mock_time: mock.MagicMock,
    ) -> None:
        """Timeout raises RuntimeError while still deleting the temporary Component CR."""
        mock_run.side_effect = [
            mock.MagicMock(stdout=""),
            mock.MagicMock(stdout="status:\n  state: COMPONENT_STATE_PENDING_RECONCILIATION\n"),
            mock.MagicMock(stdout=""),
        ]

        with self.assertRaisesRegex(RuntimeError, "Timed out waiting for xmc-bridge"):
            test_e2e_flow.verify_cms_bridge_reconciliation()

        self.assertEqual(mock_run.call_count, 3)
        mock_sleep.assert_called_once_with(1.0)
        self.assertIn("delete", mock_run.call_args_list[2].args[0])


if __name__ == "__main__":
    unittest.main()
