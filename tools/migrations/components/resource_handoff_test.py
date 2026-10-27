# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for resource_handoff."""

import subprocess
import unittest
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.migrations.components import resource_handoff


def _owned(
    release: str = "xmc", namespace: str = "tenant-a", *, managed_by: str = "Helm"
) -> dict[str, Any]:
    return {
        "metadata": {
            "labels": {"app.kubernetes.io/managed-by": managed_by} if managed_by else {},
            "annotations": {
                "meta.helm.sh/release-name": release,
                "meta.helm.sh/release-namespace": namespace,
            },
        }
    }


class TestResourceHandoff(unittest.TestCase):
    """Tests for gateway Helm ownership handoff and pre/post-adoption undo guards."""

    @patch("tools.migrations.components.resource_handoff.annotate_resource")
    @patch("tools.migrations.components.resource_handoff.fetch_live_resource")
    def test_handoff_annotates_deployment_service_hpa_and_netpol(
        self, mock_fetch: MagicMock, mock_annotate: MagicMock
    ) -> None:
        mock_fetch.side_effect = [
            _owned(),  # hpa
            _owned(),  # deployment
            _owned("xmc-cmp-ingestion-gateway"),  # service (idempotent)
            _owned(),  # networkpolicy
        ]
        res = resource_handoff.execute_resource_handoff("xmc", "tenant-a", undo=False)
        self.assertEqual(res["action"], "handoff")
        self.assertEqual(res["target_owner"], "xmc-cmp-ingestion-gateway")
        self.assertEqual(len(res["updated_resources"]), 4)
        self.assertEqual(mock_annotate.call_count, 4)
        self.assertIn("helm.sh/resource-policy=keep", mock_annotate.call_args_list[0][0][3])

    @patch("tools.migrations.components.resource_handoff.run_command")
    @patch("tools.migrations.components.resource_handoff.annotate_resource")
    @patch("tools.migrations.components.resource_handoff.fetch_live_resource")
    @patch("tools.migrations.components.resource_handoff.helm_release_exists")
    def test_handoff_undo_active_cr_refused_and_orphaned_release_uninstalled_safely(
        self,
        mock_exists: MagicMock,
        mock_fetch: MagicMock,
        mock_annotate: MagicMock,
        mock_run: MagicMock,
    ) -> None:
        # 1. Active Component CR present -> refused!
        mock_exists.return_value = True
        mock_fetch.side_effect = [
            None,  # hpa
            _owned("xmc-cmp-ingestion-gateway"),  # deployment
            _owned("xmc-cmp-ingestion-gateway"),  # service
            {"kind": "Component"},  # Component/ingestion-gateway still exists!
        ]
        with self.assertRaises(HandledError):
            resource_handoff.execute_resource_handoff("xmc", "tenant-a", undo=True)

        # 2. Orphaned release (Component CR absent) -> stamps keep, helm uninstalls, then reverts!
        mock_annotate.reset_mock()
        mock_fetch.side_effect = [
            None,  # hpa absent
            _owned("xmc-cmp-ingestion-gateway"),  # deployment
            _owned("xmc-cmp-ingestion-gateway"),  # service
            None,  # Component/ingestion-gateway absent (orphaned!)
            {"kind": "NetworkPolicy"},  # component netpol exists -> kept before uninstall
            None,  # legacy netpol absent
        ]
        mock_run.return_value = subprocess.CompletedProcess(args=[], returncode=0)
        res = resource_handoff.execute_resource_handoff("xmc", "tenant-a", undo=True)
        self.assertEqual(res["action"], "handoff_undo")
        self.assertEqual(res["target_owner"], "xmc")
        mock_run.assert_called_once()
        self.assertEqual(
            mock_run.call_args[0][0],
            ["helm", "uninstall", "xmc-cmp-ingestion-gateway", "-n", "tenant-a"],
        )
        # First 3 annotate calls stamp keep; final 2 revert owner to xmc and strip keep.
        self.assertEqual(mock_annotate.call_count, 5)
        self.assertEqual(mock_annotate.call_args_list[0][0][3], ["helm.sh/resource-policy=keep"])
        self.assertIn("helm.sh/resource-policy-", mock_annotate.call_args_list[-1][0][3])

    @patch("tools.migrations.components.resource_handoff.fetch_live_resource")
    def test_handoff_rejects_missing_managed_by_label_or_third_owner(
        self, mock_fetch: MagicMock
    ) -> None:
        with self.assertRaises(HandledError):
            resource_handoff.execute_resource_handoff("xmc", "BAD_NS", undo=False)

        mock_fetch.side_effect = [None, _owned("xmc", "tenant-a", managed_by="")]
        with self.assertRaises(HandledError):
            resource_handoff.execute_resource_handoff("xmc", "tenant-a", undo=False)

        mock_fetch.side_effect = [None, _owned("other-release", "tenant-a")]
        with self.assertRaises(HandledError):
            resource_handoff.execute_resource_handoff("xmc", "tenant-a", undo=False)


if __name__ == "__main__":
    unittest.main()
