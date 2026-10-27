# Copyright 2026 Google LLC
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for migrate CLI."""

import argparse
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.migrations.components import migrate


class TestMigrateCli(unittest.TestCase):
    """Tests for migrate.py CLI argument parsing and subcommand dispatch."""

    @patch("tools.migrations.components.migrate.finalize_adoption")
    @patch("tools.migrations.components.migrate.execute_resource_handoff")
    @patch("tools.migrations.components.migrate.prepare_profile_values")
    def test_prepare_handoff_and_finalize_dispatch(
        self, mock_prepare: MagicMock, mock_handoff: MagicMock, mock_finalize: MagicMock
    ) -> None:
        mock_prepare.return_value = {"status": "prepared"}
        mock_handoff.return_value = {"status": "handed_off"}
        mock_finalize.return_value = {"status": "finalized"}
        parser = migrate.build_parser()

        prep_args = parser.parse_args(
            ["prepare", "--release", "xmc-custom", "-n", "tenant-a", "-o", "/tmp/pv.yaml"]
        )
        self.assertEqual(migrate.dispatch(prep_args), {"status": "prepared"})
        mock_prepare.assert_called_once_with(
            "xmc-custom",
            "tenant-a",
            Path("/tmp/pv.yaml"),
            Path(prep_args.profile_dir),
            migrate._REPO_ROOT,
        )

        hoff_args = parser.parse_args(
            ["handoff", "--release", "xmc-custom", "-n", "tenant-a", "--undo"]
        )
        self.assertEqual(migrate.dispatch(hoff_args), {"status": "handed_off"})
        mock_handoff.assert_called_once_with("xmc-custom", "tenant-a", undo=True)

        fin_args = parser.parse_args(
            [
                "finalize",
                "--release",
                "xmc-custom",
                "-n",
                "tenant-a",
                "--timeout",
                "120",
                "--metrics-url",
                "http://127.0.0.1:8080/api/v1/metrics",
                "--test-xid",
                "7",
            ]
        )
        self.assertEqual(migrate.dispatch(fin_args), {"status": "finalized"})
        mock_finalize.assert_called_once_with(
            "xmc-custom",
            "tenant-a",
            timeout_sec=120,
            metrics_url="http://127.0.0.1:8080/api/v1/metrics",
            test_xid=7,
            skip_probe=False,
        )

        with self.assertRaises(HandledError):
            migrate.dispatch(argparse.Namespace(subcommand="unknown"))


if __name__ == "__main__":
    unittest.main()
