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

    @patch("tools.migrations.components.migrate.prepare_profile_values")
    def test_prepare_subcommand_dispatch_and_unsupported_guard(
        self, mock_prepare: MagicMock
    ) -> None:
        mock_prepare.return_value = {"status": "ok"}
        parser = migrate.build_parser()
        args = parser.parse_args(
            ["prepare", "--release", "xmc-custom", "-n", "tenant-a", "-o", "/tmp/pv.yaml"]
        )
        got = migrate.dispatch(args)
        self.assertEqual(got, {"status": "ok"})
        mock_prepare.assert_called_once_with("xmc-custom", "tenant-a", Path("/tmp/pv.yaml"))
        with self.assertRaises(HandledError):
            migrate.dispatch(argparse.Namespace(subcommand="unknown"))


if __name__ == "__main__":
    unittest.main()
