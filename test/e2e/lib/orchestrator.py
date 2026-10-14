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

"""End-to-end lifecycle orchestration and teardown."""

from __future__ import annotations

import concurrent.futures
import contextlib
import functools
import logging
import os
import shutil
import time
from collections.abc import Callable, Generator, Sequence
from typing import Any

from test.e2e.lib import cancellation
from test.e2e.lib.config import E2EConfig
from test.e2e.lib.helm_deployer import HelmDeployer
from test.e2e.lib.image_builder import ImageBuilder
from test.e2e.lib.kind import KindClusterManager
from test.e2e.lib.phases import Phase, phase_scope
from test.e2e.lib.spanner import SpannerManager
from test.e2e.lib.test_executor import PlaywrightTestExecutor

logger = logging.getLogger("xmc_e2e_runner")


def _run_in_phase(phase: Phase, task: Callable[[], Any]) -> Any:
    """Runs ``task`` and tags any failure with ``phase``."""
    with phase_scope(phase):
        return task()


class E2EOrchestrator:
    """Coordinates the full E2E setup, execution, and teardown.

    Attributes:
        images_built: Whether the container images were built, so that a retry can reuse
            them. Final once ``run`` returns or raises.
    """

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.spanner = SpannerManager(config)
        self.kind = KindClusterManager(config)
        self.builder = ImageBuilder(config)
        self.helm = HelmDeployer(config)
        self.tester = PlaywrightTestExecutor(config)
        self.images_built = False
        self._abandoned_executors: list[concurrent.futures.ThreadPoolExecutor] = []

    @contextlib.contextmanager
    def managed_lifecycle(self) -> Generator[None, None, None]:
        """Runs a best-effort teardown after a success, a failure, or a stop."""
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        try:
            yield
        finally:
            signum = cancellation.CONTROLLER.stop_signal
            if signum is not None:
                logger.warning("Stopped by signal %d; tearing down.", signum)
            self._join_abandoned_executors()
            logger.info("=== Executing Teardown ===")
            try:
                if not self.cfg.keep_cluster:
                    self.kind.teardown()
                else:
                    logger.info("Preserving KinD cluster %s for debugging.", self.cfg.cluster_name)
            except Exception as e:
                logger.warning("Kind teardown failed: %s", e)
            try:
                if not self.cfg.keep_cluster:
                    self.spanner.teardown()
                else:
                    logger.info(
                        "[Spanner] Preserving ephemeral databases %s and %s for debugging.",
                        self.cfg.ess_db,
                        self.cfg.xid_db,
                    )
            except Exception as e:
                logger.warning("Spanner teardown failed: %s", e)
            if not self.cfg.keep_cluster and os.path.exists(self.cfg.temp_dir):
                logger.info("Cleaning up temporary directory %s...", self.cfg.temp_dir)
                shutil.rmtree(self.cfg.temp_dir, ignore_errors=True)

    def run(self) -> None:
        """Executes the full end-to-end lifecycle.

        Raises:
            PhaseError: If a lifecycle phase fails, with the original exception as its
                ``__cause__``. Teardown has completed before this propagates.
            HarnessStoppedError: If the harness was stopped. Teardown has completed as well.
        """
        start_time = time.time()
        logger.info("================================================================")
        logger.info(" Starting XMC Headless E2E Test Suite")
        logger.info(" Cluster:       %s", self.cfg.cluster_name)
        logger.info(" Project:       %s", self.cfg.project_id)
        logger.info(" Instance:      %s", self.cfg.spanner_instance)
        logger.info(" ESS Database:  %s", self.cfg.ess_db)
        logger.info(" XID Database:  %s", self.cfg.xid_db)
        logger.info(" Screenshots:   %s", self.cfg.screenshots_dir)
        logger.info("================================================================")

        with self.managed_lifecycle():
            self._run_phases()

        elapsed = time.time() - start_time
        logger.info("================================================================")
        logger.info("🎉 E2E Test Suite Succeeded in %.2f seconds! 🎉", elapsed)
        logger.info("================================================================")

    def _run_phases(self) -> None:
        """Runs the setup phases and then the test phase, tagging failures with their phase."""
        logger.info("=== Phase 1: Building Container Images & Initializing KinD Cluster ===")
        images, _ = self._run_concurrently(
            [(Phase.IMAGE_BUILD, self._build_images), (Phase.KIND_CREATE, self.kind.setup)]
        )

        logger.info("=== Phase 2: Loading Container Images & Provisioning Spanner Databases ===")
        self._run_concurrently(
            [
                (Phase.IMAGE_LOAD, functools.partial(self.kind.load_images, images)),
                (Phase.SPANNER_SETUP, self.spanner.setup),
            ]
        )

        with phase_scope(Phase.HELM_DEPLOY):
            self.helm.deploy()
        with phase_scope(Phase.TESTS):
            self._execute_tests()

    def _execute_tests(self) -> None:
        """Runs the Playwright suite, capturing cluster diagnostics before teardown on failure."""
        try:
            self.tester.execute()
        except Exception:
            self.helm.capture_diagnostics()
            raise

    def _build_images(self) -> list[str]:
        """Builds the container images and records that they were built."""
        images = self.builder.build_all()
        self.images_built = True
        return images

    def _run_concurrently(self, tasks: Sequence[tuple[Phase, Callable[[], Any]]]) -> list[Any]:
        """Runs phase-tagged tasks in parallel and returns their results in task order.

        When a task fails or the harness is stopped, the remaining tasks are halted and
        abandoned without waiting for them; ``managed_lifecycle`` joins them before teardown.

        Args:
            tasks: Pairs of the lifecycle phase and the zero-argument callable implementing it.

        Returns:
            The task results, in the order of ``tasks``.

        Raises:
            PhaseError: For the first task that fails.
            HarnessStoppedError: If the harness was stopped.
        """
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=len(tasks))
        futures = [executor.submit(_run_in_phase, phase, task) for phase, task in tasks]
        try:
            for future in concurrent.futures.as_completed(futures):
                future.result()
        except BaseException:
            cancellation.CONTROLLER.abort_attempt()
            executor.shutdown(wait=False, cancel_futures=True)
            self._abandoned_executors.append(executor)
            raise
        executor.shutdown()
        return [future.result() for future in futures]

    def _join_abandoned_executors(self) -> None:
        """Waits for the halted tasks of a failed phase, so that none outlives the attempt."""
        for executor in self._abandoned_executors:
            executor.shutdown(wait=True)
        self._abandoned_executors.clear()
