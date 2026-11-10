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

"""Unit tests for LocalRunner orchestrator."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.local_runner import LocalRunner, service_to_k8s_deployment_name


class TestLocalRunner(unittest.TestCase):
    """Test suite for LocalRunner."""

    def test_service_to_k8s_deployment_name(self) -> None:
        self.assertEqual(service_to_k8s_deployment_name("actuator"), "actuator-deployment")
        self.assertEqual(
            service_to_k8s_deployment_name("experiment_state_server"),
            "experiment-state-server-deployment",
        )
        self.assertEqual(service_to_k8s_deployment_name("gateway_proxy"), "gateway-proxy")
        self.assertEqual(service_to_k8s_deployment_name("ui"), "ui-deployment")

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.tmp_dir.name)
        self.config = LocalDevConfig(
            project_id="test-proj",
            environment="local-dev",
            spanner_instance="test-instance",
            ess_database="test_ess_db",
            xid_database="test_xid_db",
            cluster_name="test-kind-cluster",
        )
        self.runner = LocalRunner(config=self.config, repo_root=self.temp_path)

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest", "img2:latest"]

        self.runner.up(recreate=True, skip_build=False)

        mock_preflight.assert_called_once()
        self.runner.spanner_mgr.setup.assert_called_once()
        self.runner.kind_mgr.create_cluster.assert_called_once_with(recreate=True)
        self.runner.kind_mgr.ensure_secrets.assert_called_once()
        self.runner.kind_mgr.apply_manifests.assert_called_once()
        self.runner.image_builder.build_images.assert_called_once_with(skip_build=False)
        self.runner.image_builder.load_images_into_kind.assert_called_once_with(
            ["img1:latest", "img2:latest"]
        )
        self.runner.helm_deployer.deploy.assert_called_once()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration_skip_build(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest"]
        self.runner.up(skip_build=True)

        mock_preflight.assert_called_once()
        self.runner.image_builder.build_images.assert_called_once_with(skip_build=True)
        self.runner.image_builder.load_images_into_kind.assert_not_called()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration_skip_helm(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest"]
        self.runner.up(skip_helm=True)

        mock_preflight.assert_called_once()
        self.runner.helm_deployer.deploy.assert_not_called()

    @patch("tools.dev_env.local_runner.log_success")
    @patch("tools.dev_env.local_runner.log_info")
    def test_down_without_drop_db(self, mock_info: MagicMock, mock_success: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.spanner_mgr = MagicMock()

        self.runner.down(drop_db=False)

        self.runner.kind_mgr.delete_cluster.assert_called_once()
        self.runner.spanner_mgr.drop_databases.assert_not_called()
        mock_success.assert_called_once_with(
            "Local KinD development environment teardown complete."
        )

    @patch("tools.dev_env.local_runner.log_success")
    @patch("tools.dev_env.local_runner.log_info")
    def test_down_with_drop_db(self, mock_info: MagicMock, mock_success: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.spanner_mgr = MagicMock()

        self.runner.down(drop_db=True)

        self.runner.kind_mgr.delete_cluster.assert_called_once()
        self.runner.spanner_mgr.drop_databases.assert_called_once()
        mock_success.assert_called_once_with(
            "Local KinD development environment teardown complete."
        )

    @patch("tools.dev_env.local_runner.log_info")
    def test_display_ready_banner(self, mock_log_info: MagicMock) -> None:
        self.runner._display_ready_banner()
        mock_log_info.assert_called_once()
        output = mock_log_info.call_args[0][0]
        self.assertIn("XMC Local KinD Development Environment is Ready!", output)
        self.assertIn("http://localhost:8080", output)
        self.assertIn("http://localhost:5556/dex", output)
        self.assertIn("admin@xmc.local", output)

    def test_reload_cluster_not_running(self) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.kind_mgr.exists.return_value = False
        with self.assertRaises(RuntimeError) as ctx:
            self.runner.reload(["ess"])
        self.assertIn("is not running", str(ctx.exception))

    def test_reload_invalid_service_raises(self) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.kind_mgr.exists.return_value = True
        with self.assertRaises(ValueError) as ctx:
            self.runner.reload(["nonexistent_service"])
        self.assertIn("invalid or not enabled", str(ctx.exception))

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_explicit_services(self, mock_run: MagicMock) -> None:
        """Verifies reloading explicit services triggers targeted build, load, and rollout."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.image_builder, "build_images", return_value=["reconciler_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
        ):
            self.runner.reload(services=["reconciler"])

            mock_build.assert_called_once_with(services=["reconciler"])
            mock_load.assert_called_once_with(["reconciler_image:latest"])
            self.assertTrue(mock_run.called)
            restart_call = [
                "kubectl",
                "--context",
                self.runner.kind_mgr.kube_context,
                "rollout",
                "restart",
                "deployment/reconciler-deployment",
                "-n",
                "xmc",
            ]
            status_call = [
                "kubectl",
                "--context",
                self.runner.kind_mgr.kube_context,
                "rollout",
                "status",
                "deployment/reconciler-deployment",
                "-n",
                "xmc",
                "--timeout=120s",
            ]
            mock_run.assert_any_call(restart_call, check=True)
            mock_run.assert_any_call(status_call, check=True, capture_output=True)

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_auto_detection_with_changes(self, mock_run: MagicMock) -> None:
        """Verifies auto-detection triggers rebuild and rollout of detected services."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["ui"],
            helm_upgrade_needed=False,
            modified_files=["cmd/ui/src/app.ts"],
            reasons={"ui": ["cmd/ui/src/app.ts"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(
                self.runner.image_builder, "build_images", return_value=["ui_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.helm_deployer, "deploy") as mock_helm_deploy,
        ):
            self.runner.reload(services=None)

            mock_detect.assert_called_once_with(base_ref=None)
            mock_build.assert_called_once_with(services=["ui"])
            mock_load.assert_called_once_with(["ui_image:latest"])
            mock_helm_deploy.assert_not_called()
            self.assertTrue(mock_run.called)

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_auto_detection_with_base_ref(self, mock_run: MagicMock) -> None:
        """Verifies auto-detection passes base_ref to change detector."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["ui"],
            helm_upgrade_needed=False,
            modified_files=["cmd/ui/src/app.ts"],
            reasons={"ui": ["cmd/ui/src/app.ts"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(
                self.runner.image_builder, "build_images", return_value=["ui_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.helm_deployer, "deploy") as mock_helm_deploy,
        ):
            self.runner.reload(services=None, base_ref="origin/main")

            mock_detect.assert_called_once_with(base_ref="origin/main")
            mock_build.assert_called_once_with(services=["ui"])
            mock_load.assert_called_once_with(["ui_image:latest"])
            mock_helm_deploy.assert_not_called()
            self.assertTrue(mock_run.called)

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_auto_detection_with_helm_changes(self, mock_run: MagicMock) -> None:
        """Verifies auto-detection executes helm upgrade when manifests are modified."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["reconciler"],
            helm_upgrade_needed=True,
            modified_files=["deploy/helm/values.yaml", "cmd/reconciler/reconciler.go"],
            reasons={"reconciler": ["cmd/reconciler/reconciler.go"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(self.runner.kind_mgr, "apply_manifests") as mock_apply_manifests,
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(
                self.runner.image_builder, "build_images", return_value=["reconciler_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.helm_deployer, "deploy") as mock_helm_deploy,
        ):
            self.runner.reload(services=["auto"])

            mock_detect.assert_called_once_with(base_ref=None)
            mock_helm_deploy.assert_called_once()
            mock_apply_manifests.assert_called_once()
            mock_build.assert_called_once_with(services=["reconciler"])
            mock_load.assert_called_once_with(["reconciler_image:latest"])
            self.assertTrue(mock_run.called)

    def test_reload_auto_detection_clean_working_tree(self) -> None:
        """Verifies auto-detection exits cleanly when no changes are detected."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=[],
            helm_upgrade_needed=False,
            modified_files=[],
            reasons={},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(self.runner.image_builder, "build_and_load_into_kind") as mock_build_load,
            patch.object(self.runner.helm_deployer, "deploy") as mock_helm_deploy,
        ):
            self.runner.reload(services=None)

            mock_detect.assert_called_once()
            mock_build_load.assert_not_called()
            mock_helm_deploy.assert_not_called()

    def test_reload_all_services(self) -> None:
        """Verifies reloading 'all' rebuilds all enabled services."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.image_builder, "build_images", return_value=["img1", "img2"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.change_detector, "detect_changes") as mock_detect,
            patch("tools.dev_env.local_runner.run_command"),
        ):
            self.runner.reload(services=["all"])

            mock_detect.assert_not_called()
            mock_build.assert_called_once_with(
                services=sorted(self.runner.change_detector.enabled_services)
            )
            mock_load.assert_called_once_with(["img1", "img2"])

    def test_reload_mixed_services_with_auto(self) -> None:
        """Verifies containing 'auto' in services triggers auto-detection."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["actuator"],
            helm_upgrade_needed=False,
            modified_files=["cmd/actuator/actuator.go"],
            reasons={"actuator": ["cmd/actuator/actuator.go"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(
                self.runner.image_builder, "build_images", return_value=["actuator_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch("tools.dev_env.local_runner.run_command"),
        ):
            self.runner.reload(services=["reconciler", "auto"])

            mock_detect.assert_called_once()
            mock_build.assert_called_once_with(services=["actuator", "reconciler"])
            mock_load.assert_called_once_with(["actuator_image:latest"])

    def test_reload_disabled_service_raises_error(self) -> None:
        """Verifies reloading a disabled or invalid service raises ValueError."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            self.assertRaises(ValueError),
        ):
            self.runner.reload(services=["invalid_service_foo"])

    def test_reload_disabled_service_with_all_raises_error(self) -> None:
        """Verifies invalid service raises ValueError even when 'all' is present."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            self.assertRaises(ValueError),
        ):
            self.runner.reload(services=["all", "invalid_service_foo"])

    def test_reload_mixed_services_with_all(self) -> None:
        """Verifies containing 'all' in services targets all enabled services."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.image_builder, "build_images", return_value=["img1", "img2"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.change_detector, "detect_changes") as mock_detect,
            patch("tools.dev_env.local_runner.run_command"),
        ):
            self.runner.reload(services=["reconciler", "all"])

            mock_detect.assert_not_called()
            mock_build.assert_called_once()
            mock_load.assert_called_once_with(["img1", "img2"])

    @patch("tools.dev_env.local_runner.log_info")
    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_rollout_failure_handled_gracefully(
        self, mock_run: MagicMock, mock_log_info: MagicMock
    ) -> None:
        """Verifies rollout command failures are caught, logged, and raise RuntimeError."""
        mock_run.side_effect = RuntimeError("kubectl rollout failed")
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.image_builder, "build_images", return_value=["reconciler_image:latest"]
            ),
            patch.object(self.runner.image_builder, "load_images_into_kind"),
        ):
            with self.assertRaises(RuntimeError) as ctx:
                self.runner.reload(services=["reconciler"])
            self.assertIn("failure(s)", str(ctx.exception))
            mock_log_info.assert_any_call(
                "Failed to restart deployment 'reconciler-deployment': kubectl rollout failed. Skipping."
            )

    def test_reload_uppercase_keywords(self) -> None:
        """Verifies uppercase 'ALL' and 'AUTO' are handled correctly."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.image_builder, "build_images", return_value=["img1", "img2"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch.object(self.runner.change_detector, "detect_changes") as mock_detect,
            patch("tools.dev_env.local_runner.run_command"),
        ):
            self.runner.reload(services=["ALL"])

            mock_detect.assert_not_called()
            mock_build.assert_called_once()
            mock_load.assert_called_once_with(["img1", "img2"])

    def test_reload_not_running_raises_error(self) -> None:
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=False),
            self.assertRaises(RuntimeError),
        ):
            self.runner.reload()

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_only_helm(self, mock_run: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.kind_mgr.exists.return_value = True
        self.runner.kind_mgr.kube_context = "kind-test-cluster"
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        result = self.runner.reload([], helm=True)
        self.assertTrue(result)
        self.runner.helm_deployer.deploy.assert_called_once()
        self.runner.kind_mgr.apply_manifests.assert_called_once()

    @patch("tools.dev_env.local_runner.run_command")
    def test_reload_with_failures_raises(self, mock_run: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.kind_mgr.exists.return_value = True
        self.runner.kind_mgr.kube_context = "kind-test-cluster"
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()
        self.runner.image_builder.build_images.return_value = ["img:latest"]

        def run_side_effect(cmd, *args, **kwargs):
            if "status" in cmd:
                raise Exception("rollout timeout")
            return MagicMock()

        mock_run.side_effect = run_side_effect

        with self.assertRaises(RuntimeError) as ctx:
            self.runner.reload(["ess"])
        self.assertIn("failure(s)", str(ctx.exception))

    def test_reload_watch_mode_triggers_watch_loop(self) -> None:
        """Verifies reload(watch=True) dispatches to _watch_loop."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(self.runner, "_watch_loop") as mock_watch,
        ):
            self.runner.reload(services=["reconciler"], watch=True, helm=True, interval=2.0)
            mock_watch.assert_called_once_with(
                services=["reconciler"],
                interval=2.0,
                helm=True,
                base_ref=None,
            )

    def test_reload_helm_flag_triggers_helm_deploy(self) -> None:
        """Verifies reload(helm=True) triggers HelmDeployer.deploy."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(self.runner.kind_mgr, "apply_manifests") as mock_apply,
            patch.object(self.runner.helm_deployer, "deploy") as mock_deploy,
            patch.object(self.runner.image_builder, "build_images") as mock_build,
            patch.object(self.runner.change_detector, "detect_changes") as mock_detect,
        ):
            self.runner.reload(services=[], helm=True)
            mock_deploy.assert_called_once()
            mock_apply.assert_called_once()
            mock_detect.assert_not_called()
            mock_build.assert_not_called()

    def test_reload_services_with_helm_keyword(self) -> None:
        """Verifies passing 'helm' in services triggers HelmDeployer.deploy without building image."""
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(self.runner.kind_mgr, "apply_manifests") as mock_apply,
            patch.object(self.runner.helm_deployer, "deploy") as mock_deploy,
            patch.object(self.runner.image_builder, "build_images") as mock_build,
        ):
            self.runner.reload(services=["helm"])
            mock_deploy.assert_called_once()
            mock_apply.assert_called_once()
            mock_build.assert_not_called()

    def test_get_workspace_signature(self) -> None:
        """Verifies workspace signature mapping mtime and size."""
        test_file = self.temp_path / "test.txt"
        test_file.write_text("hello world")
        with patch.object(
            self.runner.change_detector,
            "get_modified_files",
            return_value=["test.txt", "nonexistent.txt"],
        ):
            sig = self.runner._get_workspace_signature()
            self.assertIn("test.txt", sig)
            self.assertGreater(sig["test.txt"][1], 0)
            self.assertEqual(sig["nonexistent.txt"], (0, 0))

    def test_reload_watch_mode_invalid_interval_raises_error(self) -> None:
        """Verifies reload(watch=True) raises ValueError when interval <= 0."""
        with patch.object(self.runner.kind_mgr, "exists", return_value=True):
            with self.assertRaisesRegex(ValueError, "Watch interval must be greater than 0"):
                self.runner.reload(watch=True, interval=0)
            with self.assertRaisesRegex(ValueError, "Watch interval must be greater than 0"):
                self.runner.reload(watch=True, interval=-1.5)

    def test_reload_explicit_services_with_changed_files_filters_services(self) -> None:
        """Verifies explicit services are filtered against change detector when changed_files is passed."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["reconciler"],
            helm_upgrade_needed=False,
            modified_files=["cmd/reconciler/main.go"],
            reasons={"reconciler": ["cmd/reconciler/main.go"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(
                self.runner.image_builder, "build_images", return_value=["reconciler_image:latest"]
            ) as mock_build,
            patch.object(self.runner.image_builder, "load_images_into_kind") as mock_load,
            patch("tools.dev_env.local_runner.run_command"),
        ):
            self.runner._reload_once(
                services=["ui", "reconciler"], changed_files=["cmd/reconciler/main.go"]
            )
            mock_detect.assert_called_once_with(files=["cmd/reconciler/main.go"])
            mock_build.assert_called_once_with(services=["reconciler"])
            mock_load.assert_called_once_with(["reconciler_image:latest"])

    def test_reload_explicit_services_with_changed_files_no_match(self) -> None:
        """Verifies no reload occurs if changed_files does not affect requested explicit services."""
        from tools.dev_env.change_detector import ChangeDetectionResult

        mock_result = ChangeDetectionResult(
            services=["ui"],
            helm_upgrade_needed=False,
            modified_files=["cmd/ui/main.ts"],
            reasons={"ui": ["cmd/ui/main.ts"]},
        )
        with (
            patch.object(self.runner.kind_mgr, "exists", return_value=True),
            patch.object(
                self.runner.change_detector, "detect_changes", return_value=mock_result
            ) as mock_detect,
            patch.object(self.runner.image_builder, "build_and_load_into_kind") as mock_build_load,
        ):
            reloaded = self.runner._reload_once(
                services=["reconciler"], changed_files=["cmd/ui/main.ts"]
            )
            self.assertFalse(reloaded)
            mock_detect.assert_called_once_with(files=["cmd/ui/main.ts"])
            mock_build_load.assert_not_called()

    @patch("tools.dev_env.local_runner.time.sleep")
    def test_watch_loop_revert_workspace_triggers_reload(self, mock_sleep: MagicMock) -> None:
        """Verifies watch loop triggers reload with reverted files when workspace becomes clean."""
        with (
            patch.object(self.runner, "_reload_once") as mock_reload,
            patch.object(
                self.runner,
                "_get_workspace_signature",
                side_effect=[
                    {"cmd/reconciler/main.go": (1, 10)},  # initial sig
                    {},  # developer reverted all changes (clean)
                    KeyboardInterrupt,
                ],
            ),
        ):
            self.runner._watch_loop(services=["reconciler"], interval=1.0)
            self.assertEqual(mock_reload.call_count, 2)
            mock_reload.assert_called_with(
                services=["reconciler"],
                changed_files=["cmd/reconciler/main.go"],
                base_ref=None,
            )

    @patch("tools.dev_env.local_runner.time.sleep")
    def test_watch_loop_commit_workspace_no_reload(self, mock_sleep: MagicMock) -> None:
        """Verifies watch loop ignores clean workspace transition when files are committed without disk change."""
        file_path = self.temp_path / "cmd/reconciler/main.go"
        file_path.parent.mkdir(parents=True, exist_ok=True)
        file_path.write_text("package main\n")
        st = file_path.stat()
        file_sig = (st.st_mtime_ns, st.st_size)

        with (
            patch.object(self.runner, "_reload_once") as mock_reload,
            patch.object(
                self.runner,
                "_get_workspace_signature",
                side_effect=[
                    {"cmd/reconciler/main.go": file_sig},
                    {},
                    KeyboardInterrupt,
                ],
            ),
        ):
            self.runner._watch_loop(services=["reconciler"], interval=1.0)
            self.assertEqual(mock_reload.call_count, 1)

    @patch("tools.dev_env.local_runner.time.sleep")
    def test_watch_loop_debounce_clean_workspace(self, mock_sleep: MagicMock) -> None:
        """Verifies watch loop triggers reload with previous dirty files if workspace becomes clean during debounce."""
        with (
            patch.object(self.runner, "_reload_once") as mock_reload,
            patch.object(
                self.runner,
                "_get_workspace_signature",
                side_effect=[
                    {"f.go": (1, 10)},  # initial sig
                    {"f.go": (2, 20)},  # change detected
                    {},  # clean after debounce
                    KeyboardInterrupt,
                ],
            ),
        ):
            self.runner._watch_loop(services=["reconciler"], interval=1.0)
            self.assertEqual(mock_reload.call_count, 2)
            mock_reload.assert_called_with(
                services=["reconciler"],
                changed_files=["f.go"],
                base_ref=None,
            )

    @patch("tools.dev_env.local_runner.run_command")
    def test_restart_deployments_component_managed_presence_check(
        self, mock_run: MagicMock
    ) -> None:
        """Skips NotFound component deployments cleanly, fails on API errors, and restarts present ones."""
        not_found_probe = MagicMock(
            returncode=1,
            stderr='Error from server (NotFound): deployments.apps "ingestion-gateway-deployment" not found',
            stdout="",
        )
        mock_run.return_value = not_found_probe
        triggered, failed = self.runner._restart_deployments("kind-ctx", ["ingestion_gateway"])
        self.assertEqual(triggered, [])
        self.assertEqual(failed, [])

        api_err_probe = MagicMock(returncode=1, stderr="connection refused", stdout="")
        mock_run.side_effect = [api_err_probe, RuntimeError("connection refused")]
        triggered_err, failed_err = self.runner._restart_deployments(
            "kind-ctx", ["ingestion_gateway"]
        )
        self.assertEqual(triggered_err, [])
        self.assertEqual(failed_err, ["ingestion-gateway-deployment"])

        present_probe = MagicMock(returncode=0, stderr="", stdout="")
        mock_run.side_effect = [present_probe, MagicMock(returncode=0)]
        triggered_ok, failed_ok = self.runner._restart_deployments(
            "kind-ctx", ["ingestion_gateway"]
        )
        self.assertEqual(triggered_ok, ["ingestion-gateway-deployment"])
        self.assertEqual(failed_ok, [])


if __name__ == "__main__":
    unittest.main()
