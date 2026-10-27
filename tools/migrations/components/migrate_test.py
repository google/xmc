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

    @patch("tools.migrations.components.migrate.execute_resource_handoff")
    @patch("tools.migrations.components.migrate.prepare_profile_values")
    def test_prepare_and_handoff_dispatch(
        self, mock_prepare: MagicMock, mock_handoff: MagicMock
    ) -> None:
        mock_prepare.return_value = {"status": "prepared"}
        mock_handoff.return_value = {"status": "handed_off"}
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

        with self.assertRaises(HandledError):
            migrate.dispatch(argparse.Namespace(subcommand="unknown"))


if __name__ == "__main__":
    unittest.main()
