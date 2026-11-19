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

"""Unit tests for tools.dev_env.change_detector."""

import subprocess
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env import change_detector
from tools.dev_env.change_detector import ChangeDetector


class ChangeDetectorTest(unittest.TestCase):
    """Test suite for ChangeDetector file mapping and git diff inspection."""

    def setUp(self) -> None:
        super().setUp()
        self.test_enabled_services = [
            "actuator",
            "components",
            "dashboard_service",
            "experiment_state_server",
            "health_service",
            "ingestion_gateway",
            "log_monitor",
            "reconciler",
            "rod_data_provider",
            "ui",
            "xid_service",
        ]
        self.detector = ChangeDetector(
            repo_root=Path("/fake/repo"),
            enabled_services=self.test_enabled_services,
        )

    def test_resolve_service_name_aliases(self) -> None:
        """Verifies canonical name and alias resolution."""
        self.assertEqual(
            change_detector.resolve_service_name("ess"),
            "experiment_state_server",
        )
        self.assertEqual(
            change_detector.resolve_service_name("ESS"),
            "experiment_state_server",
        )
        self.assertEqual(
            change_detector.resolve_service_name("xid"),
            "xid_service",
        )
        self.assertEqual(
            change_detector.resolve_service_name("gateway"),
            "ingestion_gateway",
        )
        self.assertEqual(
            change_detector.resolve_service_name("ingestion"),
            "ingestion_gateway",
        )
        self.assertEqual(
            change_detector.resolve_service_name("dashboard"),
            "dashboard_service",
        )
        self.assertEqual(
            change_detector.resolve_service_name("health"),
            "health_service",
        )
        self.assertEqual(
            change_detector.resolve_service_name("rod"),
            "rod_data_provider",
        )
        self.assertEqual(
            change_detector.resolve_service_name("monitor"),
            "log_monitor",
        )
        self.assertEqual(
            change_detector.resolve_service_name("custom_svc"),
            "custom_svc",
        )

    def test_resolve_service_names_deduplication(self) -> None:
        """Verifies resolving multiple names, deduplicating, and filtering reserved keywords."""
        input_names = [
            "ess",
            "experiment_state_server",
            "ui",
            "xid",
            "",
            "   ",
            "auto",
            "ALL",
            "  auto  ",
        ]
        resolved = change_detector.resolve_service_names(input_names)
        self.assertEqual(
            resolved,
            ["experiment_state_server", "ui", "xid_service"],
        )

    @mock.patch("subprocess.run")
    def test_get_modified_files_git_diff_and_untracked(self, mock_run: mock.MagicMock) -> None:
        """Verifies collecting modified files from the git diff and untracked file listing."""

        def side_effect(cmd, **kwargs):
            del kwargs
            if "ls-files" in cmd:
                return subprocess.CompletedProcess(
                    args=cmd,
                    returncode=0,
                    stdout="cmd/ui/src/new.ts\0",
                    stderr="",
                )
            if "diff" in cmd:
                return subprocess.CompletedProcess(
                    args=cmd,
                    returncode=0,
                    stdout=(
                        "cmd/actuator/main.go\0old.go\0cmd/xid_service/renamed.go\0"
                        "internal/common/config.go\0"
                    ),
                    stderr="",
                )
            return subprocess.CompletedProcess(args=cmd, returncode=1, stdout="", stderr="")

        mock_run.side_effect = side_effect
        files = self.detector.get_modified_files()

        self.assertIn("cmd/actuator/main.go", files)
        self.assertNotIn("md/actuator/main.go", files)
        self.assertIn("cmd/ui/src/new.ts", files)
        self.assertIn("cmd/xid_service/renamed.go", files)
        self.assertIn("old.go", files)
        self.assertIn("internal/common/config.go", files)
        self.assertEqual(files, sorted(files))
        for call in mock_run.call_args_list:
            self.assertEqual(call.kwargs["cwd"], "/fake/repo")

    @mock.patch("subprocess.run")
    def test_get_modified_files_with_base_ref_diffs_against_merge_base(
        self, mock_run: mock.MagicMock
    ) -> None:
        """Verifies base_ref changes are listed relative to its merge base with HEAD."""
        outputs = {"merge-base": "abc123\n", "diff": "internal/common/config.go\0"}
        mock_run.side_effect = lambda cmd, **kwargs: subprocess.CompletedProcess(
            args=cmd, returncode=0, stdout=outputs.get(cmd[1], ""), stderr=""
        )

        files = self.detector.get_modified_files(base_ref="origin/main")

        self.assertEqual(files, ["internal/common/config.go"])
        commands = [call.args[0] for call in mock_run.call_args_list]
        self.assertIn(["git", "merge-base", "origin/main", "HEAD"], commands)
        self.assertIn(
            ["git", "diff", "--name-only", "--no-renames", "-z", "abc123", "--"], commands
        )

    @mock.patch("subprocess.run")
    def test_get_modified_files_git_failure_handled(self, mock_run: mock.MagicMock) -> None:
        """Verifies git failures return an empty list gracefully."""
        mock_run.side_effect = OSError("git not found")
        files = self.detector.get_modified_files()
        self.assertEqual(files, [])

    @mock.patch("subprocess.run")
    def test_get_modified_files_git_nonzero_exit_code(self, mock_run: mock.MagicMock) -> None:
        """Verifies non-zero git exit codes return an empty list and log a warning."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["git", "diff"],
            returncode=128,
            stdout="",
            stderr="fatal: not a git repository",
        )
        with self.assertLogs("xmc.dev_env.change_detector", level="WARNING") as logs:
            files = self.detector.get_modified_files()
        self.assertEqual(files, [])
        self.assertIn("fatal: not a git repository", logs.output[0])

    def test_detect_changes_service_specific_files(self) -> None:
        """Verifies individual microservice file changes map to their respective service."""
        test_cases = [
            (["cmd/experiment_state_server/main.go"], ["experiment_state_server"]),
            (["cmd/xid_service/xid.go"], ["xid_service"]),
            (["cmd/actuator/actuator.go"], ["actuator"]),
            (["cmd/reconciler/reconciler.go"], ["reconciler"]),
            (["cmd/ui/src/app/dashboard.ts"], ["ui"]),
            (["cmd/ingestion_gateway/main.go"], ["ingestion_gateway"]),
            (["cmd/dashboard_service/main.go"], ["dashboard_service"]),
            (["cmd/health_service/main.go"], ["health_service"]),
            (["cmd/rod_data_provider/jobs.go"], ["rod_data_provider"]),
            (["cmd/log_monitor/monitor.go"], ["log_monitor"]),
            (["cmd/components/main.go"], ["components"]),
            (["cmd/xmc_bridge/main.go"], ["components"]),
            (["cmd/actuator/BUILD.bazel"], ["actuator"]),
        ]

        for modified_files, expected_services in test_cases:
            result = self.detector.detect_changes(files=modified_files)
            self.assertEqual(
                result.services,
                expected_services,
                f"Failed for files: {modified_files}",
            )
            self.assertFalse(result.helm_upgrade_needed)

    def test_detect_changes_shared_code(self) -> None:
        """Verifies changes in shared packages trigger all enabled services."""
        shared_files = [
            "internal/common/config.go",
            "internal/auth/auth.go",
            "internal/configutil/util.go",
            "internal/security/tls.go",
            "internal/logging/log.go",
            "internal/kubernetes/client.go",
            "internal/task_queue/queue.go",
            "internal/storage/spanner/migrations/ess/001.sql",
            "internal/query_generator/query.go",
            "internal/dashboard/service.go",
            "api/protos/components/component.proto",
            "api/protos/experiment/v1/experiment.proto",
            "api/protos/xid/v1/xid.proto",
            "api/protos/metrics/metrics.proto",
            "api/protos/dashboard/dashboard.proto",
            "api/protos/health/health.proto",
            "api/protos/rod/v1/rod.proto",
            "pkg/apis/v1/types.go",
        ]
        for f in shared_files:
            result = self.detector.detect_changes(files=[f])
            self.assertEqual(
                set(result.services),
                set(self.detector.enabled_services),
            )
            self.assertFalse(result.helm_upgrade_needed)
            self.assertEqual(
                result.migrations_needed,
                f.endswith(".sql") and f.startswith("internal/storage/spanner/"),
            )

    def test_detect_changes_shared_root_files(self) -> None:
        """Verifies changes to root and shared build files trigger all enabled services."""
        for root_file in [
            "go.mod",
            "go.sum",
            "WORKSPACE",
            "MODULE.bazel",
            "BUILD.bazel",
            "BUILD",
            ".bazelrc",
            "internal/BUILD.bazel",
            "api/BUILD",
        ]:
            result = self.detector.detect_changes(files=[root_file])
            self.assertEqual(
                set(result.services),
                set(self.detector.enabled_services),
            )

    def test_detect_changes_helm_and_manifests(self) -> None:
        """Verifies Helm and Kubernetes manifest changes flag helm_upgrade_needed."""
        helm_files = [
            "deploy/helm/values.yaml",
            "deploy/helm/templates/reconciler/deployment.yaml",
            "deploy/local/gateway_proxy.yaml",
            "test/e2e/manifests/redis.yaml",
        ]
        result = self.detector.detect_changes(files=helm_files)
        self.assertTrue(result.helm_upgrade_needed)
        self.assertEqual(result.services, [])

    def test_detect_changes_scoped_to_enabled_services(self) -> None:
        """Verifies detection respects configured enabled_services."""
        scoped_detector = ChangeDetector(
            repo_root=Path("/fake/repo"),
            enabled_services=["actuator", "ui"],
        )
        # Change in xid should not trigger xid since it's disabled
        result = scoped_detector.detect_changes(files=["cmd/xid_service/main.go"])
        self.assertEqual(result.services, [])

        # Shared change should only trigger enabled services
        shared_result = scoped_detector.detect_changes(files=["internal/common/util.go"])
        self.assertEqual(shared_result.services, ["actuator", "ui"])

    def test_detect_changes_non_code_files(self) -> None:
        """Verifies markdown or docs changes do not trigger reloads."""
        result = self.detector.detect_changes(
            files=["README.md", "docs/architecture.md", "deploy/local/README.md"]
        )
        self.assertEqual(result.services, [])
        self.assertFalse(result.helm_upgrade_needed)

    def test_detector_defaults_to_discovered_services(self) -> None:
        """Verifies ChangeDetector without enabled_services discovers from repo."""
        with mock.patch(
            "tools.dev_env.change_detector.discover_buildable_services",
            return_value=["foo", "bar"],
        ):
            detector = ChangeDetector(repo_root=Path("/fake/repo"))
            self.assertEqual(detector.enabled_services, ["foo", "bar"])


if __name__ == "__main__":
    unittest.main()
