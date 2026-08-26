#!/usr/bin/env python3
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

"""Unit tests for tools.dev_env.config module."""

import argparse
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tools.dev_env import config


class ConfigResolutionTest(unittest.TestCase):
    """Test cases for configuration path resolution and precedence loading."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.temp_dir.name)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()
        super().tearDown()

    def test_resolve_config_path_explicit(self) -> None:
        """Verifies explicitly specified configuration path is resolved."""
        cfg_file = self.root / "custom-config.yaml"
        cfg_file.write_text("vars:\n  project_id: test-p\n", encoding="utf-8")

        resolved = config.resolve_config_path(str(cfg_file), repo_root=self.root)
        self.assertEqual(resolved, cfg_file.resolve())

    def test_resolve_config_path_explicit_relative(self) -> None:
        """Verifies relative explicit path resolves against repo_root."""
        (self.root / "deploy" / "local").mkdir(parents=True, exist_ok=True)
        cfg_file = self.root / "deploy" / "local" / "custom.yaml"
        cfg_file.write_text("vars:\n  project_id: test-p\n", encoding="utf-8")

        resolved = config.resolve_config_path("deploy/local/custom.yaml", repo_root=self.root)
        self.assertEqual(resolved, cfg_file.resolve())

    def test_resolve_config_path_explicit_not_found(self) -> None:
        """Verifies FileNotFoundError is raised when explicit path does not exist."""
        with self.assertRaises(FileNotFoundError):
            config.resolve_config_path("non-existent.yaml", repo_root=self.root)

    def test_resolve_config_path_defaults(self) -> None:
        """Verifies default configuration locations are checked in order."""
        ctk_cfg = self.root / "deploy" / "ctk_v2" / "xmanager-cloud-deployment.yaml"
        ctk_cfg.parent.mkdir(parents=True, exist_ok=True)
        ctk_cfg.write_text("vars:\n  project_id: ctk-p\n", encoding="utf-8")

        resolved = config.resolve_config_path(repo_root=self.root)
        self.assertEqual(resolved, ctk_cfg.resolve())

    def test_resolve_config_path_none_when_empty(self) -> None:
        """Verifies None is returned when no default configuration files exist."""
        resolved = config.resolve_config_path(repo_root=self.root)
        self.assertIsNone(resolved)

    def test_resolve_config_path_empty_or_whitespace_string_fallback(self) -> None:
        """Verifies empty or whitespace config_path falls back to default locations."""
        ctk_cfg = self.root / "deploy" / "ctk_v2" / "xmanager-cloud-deployment.yaml"
        ctk_cfg.parent.mkdir(parents=True, exist_ok=True)
        ctk_cfg.write_text("vars:\n  project_id: ctk-p\n", encoding="utf-8")

        self.assertEqual(
            config.resolve_config_path("", repo_root=self.root),
            ctk_cfg.resolve(),
        )
        self.assertEqual(
            config.resolve_config_path("   ", repo_root=self.root),
            ctk_cfg.resolve(),
        )

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_precedence_cli(self) -> None:
        """Verifies CLI arguments take highest precedence over Env and YAML."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n",
            encoding="utf-8",
        )

        with patch.dict(
            "os.environ",
            {"PROJECT_ID": "env-p", "XMANAGER_ENV": "env-env"},
            clear=True,
        ):
            args = argparse.Namespace(
                config=str(cfg_file),
                project_id="cli-p",
                environment="cli-env",
            )
            cfg = config.load_config(args, repo_root=self.root)
            self.assertEqual(cfg.project_id, "cli-p")
            self.assertEqual(cfg.environment, "cli-env")
            self.assertEqual(cfg.config_path, cfg_file.resolve())

    def test_load_config_precedence_env(self) -> None:
        """Verifies Environment variables override YAML variables and defaults."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n",
            encoding="utf-8",
        )

        with patch.dict(
            "os.environ",
            {"PROJECT_ID": "env-p", "XMANAGER_ENV": "env-env"},
            clear=True,
        ):
            args = argparse.Namespace(config=str(cfg_file))
            cfg = config.load_config(args, repo_root=self.root)
            self.assertEqual(cfg.project_id, "env-p")
            self.assertEqual(cfg.environment, "env-env")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_precedence_yaml(self) -> None:
        """Verifies YAML values are used when CLI and Env variables are unset."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text(
            "vars:\n  project_id: yaml-p\n  environment: yaml-env\n",
            encoding="utf-8",
        )

        args = argparse.Namespace(config=str(cfg_file))
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "yaml-p")
        self.assertEqual(cfg.environment, "yaml-env")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_defaults(self) -> None:
        """Verifies default values when only project_id is provided via CLI."""
        args = argparse.Namespace(project_id="my-proj")
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "my-proj")
        self.assertEqual(cfg.environment, "dev")
        self.assertIsNone(cfg.config_path)

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_empty_strings_fallback_properly(self) -> None:
        """Verifies empty or whitespace values fallback cleanly without producing literal 'None'."""
        cfg_file = self.root / "config.yaml"
        cfg_file.write_text("vars:\n  project_id: fallback-p\n", encoding="utf-8")

        args = argparse.Namespace(
            config=str(cfg_file),
            project_id="   ",
            environment="",
        )
        cfg = config.load_config(args, repo_root=self.root)
        self.assertEqual(cfg.project_id, "fallback-p")
        self.assertEqual(cfg.environment, "dev")

    @patch.dict("os.environ", {}, clear=True)
    def test_load_config_missing_project_id_raises_error(self) -> None:
        """Verifies ValueError is raised when project_id cannot be resolved."""
        args = argparse.Namespace()
        with self.assertRaises(ValueError) as cm:
            config.load_config(args, repo_root=self.root)
        self.assertIn("Missing required configuration field: 'project_id'", str(cm.exception))


class LocalDevConfigDataClassTest(unittest.TestCase):
    """Test cases for LocalDevConfig dataclass validation."""

    def test_valid_config(self) -> None:
        """Verifies instantiation with valid required fields."""
        cfg = config.LocalDevConfig(project_id="test-proj", environment="staging")
        self.assertEqual(cfg.project_id, "test-proj")
        self.assertEqual(cfg.environment, "staging")

    def test_empty_project_id_raises_value_error(self) -> None:
        """Verifies whitespace or empty project_id raises ValueError."""
        with self.assertRaises(ValueError):
            config.LocalDevConfig(project_id="")
        with self.assertRaises(ValueError):
            config.LocalDevConfig(project_id="   ")


if __name__ == "__main__":
    unittest.main()
