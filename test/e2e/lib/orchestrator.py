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
import logging
import os
import shutil
import signal
import sys
import time
from collections.abc import Generator
from typing import Any

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.helm_deployer import HelmDeployer
from test.e2e.lib.image_builder import ImageBuilder
from test.e2e.lib.kind import KindClusterManager
from test.e2e.lib.spanner import SpannerManager
from test.e2e.lib.test_executor import PlaywrightTestExecutor

logger = logging.getLogger("xmc_e2e_runner")


class E2EOrchestrator:
    """Coordinates the full E2E setup, execution, and teardown."""

    def __init__(self, config: E2EConfig) -> None:
        self.cfg = config
        self.spanner = SpannerManager(config)
        self.kind = KindClusterManager(config)
        self.builder = ImageBuilder(config)
        self.helm = HelmDeployer(config)
        self.tester = PlaywrightTestExecutor(config)

    @contextlib.contextmanager
    def managed_lifecycle(self) -> Generator[None, None, None]:
        """Guarantees cleanup on success, failure, or interrupt."""
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        try:
            yield
        finally:
            logger.info("=== Executing Teardown ===")
            try:
                self.kind.teardown()
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
        """Executes the full end-to-end lifecycle."""
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
            sys.exit(1)

        signal.signal(signal.SIGINT, _signal_handler)
        signal.signal(signal.SIGTERM, _signal_handler)

        with self.managed_lifecycle():
            logger.info("=== Phase 1: Building Container Images & Initializing KinD Cluster ===")
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                builder_future = executor.submit(self.builder.build_all)
                kind_future = executor.submit(self.kind.setup)

                images = builder_future.result()
                kind_future.result()

            logger.info(
                "=== Phase 2: Loading Container Images & Provisioning Spanner Databases ==="
            )
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                load_future = executor.submit(self.kind.load_images, images)
                spanner_future = executor.submit(self.spanner.setup)

                load_future.result()
                spanner_future.result()

            self.helm.deploy()
            self.tester.execute()

        elapsed = time.time() - start_time
        logger.info("================================================================")
        logger.info("🎉 E2E Test Suite Succeeded in %.2f seconds! 🎉", elapsed)
        logger.info("================================================================")
