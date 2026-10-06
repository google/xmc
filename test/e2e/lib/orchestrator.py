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

"""End-to-end lifecycle orchestration and signal handling."""

from __future__ import annotations

import concurrent.futures
import contextlib
import functools
import logging
import os
import shutil
import signal
import sys
import time
from collections.abc import Callable, Generator, Sequence
from typing import Any

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

    @contextlib.contextmanager
    def managed_lifecycle(self) -> Generator[None, None, None]:
        """Guarantees cleanup on success, failure, or interrupt."""
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        try:
            yield
        finally:
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
            SystemExit: With ``128 + signum`` on SIGINT or SIGTERM.
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

        def _signal_handler(signum: int, _frame: Any) -> None:
            logger.warning("Received signal %d. Triggering cleanup and exiting...", signum)
            sys.exit(128 + signum)

        signal.signal(signal.SIGINT, _signal_handler)
        signal.signal(signal.SIGTERM, _signal_handler)

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
            self.tester.execute()

    def _build_images(self) -> list[str]:
        """Builds the container images and records that they were built."""
        images = self.builder.build_all()
        self.images_built = True
        return images

    def _run_concurrently(self, tasks: Sequence[tuple[Phase, Callable[[], Any]]]) -> list[Any]:
        """Runs phase-tagged tasks in parallel and returns their results in task order.

        Args:
            tasks: Pairs of the lifecycle phase and the zero-argument callable implementing it.

        Returns:
            The task results, in the order of ``tasks``.

        Raises:
            PhaseError: For the first task that fails, once the other tasks have finished.
        """
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(tasks)) as executor:
            futures = [executor.submit(_run_in_phase, phase, task) for phase, task in tasks]
            try:
                for future in concurrent.futures.as_completed(futures):
                    future.result()
            except BaseException:
                for future in futures:
                    future.cancel()
                raise
        return [future.result() for future in futures]
