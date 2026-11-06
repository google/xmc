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

"""Runs upstream DefaultVizierServer as the dev/test Vizier fixture.

DEV/TEST ONLY: production Vizier is bring-your-own. Settings come from VIZIER_PORT and
VIZIER_DATABASE_URL (see server_env.py).
"""

import logging
import os
import signal
import threading

import grpc
import server_env
import supported_algorithms
from vizier._src.service import vizier_service, vizier_service_pb2
from vizier.service import servers

# Lets in-flight RPCs finish on SIGTERM, within the default 30s pod termination grace period.
_SHUTDOWN_GRACE_SECONDS = 10.0


def install_algorithm_guard() -> None:
    """Makes CreateStudy reject unsupported algorithms with INVALID_ARGUMENT.

    DefaultVizierServer builds its own VizierServicer and gRPC server and offers no hook to
    inject either, so the guard wraps VizierServicer.CreateStudy at class level. It must run
    before the server is constructed, because gRPC binds the servicer's methods at registration.
    """
    create_study = vizier_service.VizierServicer.CreateStudy

    def guarded_create_study(
        servicer: vizier_service.VizierServicer,
        request: vizier_service_pb2.CreateStudyRequest,
        context: grpc.ServicerContext | None = None,
    ) -> object:
        error = supported_algorithms.unsupported_algorithm_error(request.study.study_spec.algorithm)
        if error is not None:
            if context is None:
                raise ValueError(error)
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, error)  # Raises.
        return create_study(servicer, request, context)

    vizier_service.VizierServicer.CreateStudy = guarded_create_study


def wait_for_termination_signal() -> None:
    """Blocks until SIGTERM or SIGINT."""
    stop_requested = threading.Event()

    def request_stop(signum: int, _frame: object) -> None:
        logging.info("Received signal %d, shutting down", signum)
        stop_requested.set()

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    stop_requested.wait()


def main() -> None:
    """Serves Vizier until terminated."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    settings = server_env.load_server_env(os.environ)
    install_algorithm_guard()
    server = servers.DefaultVizierServer(
        host="0.0.0.0", port=settings.port, database_url=settings.database_url
    )
    logging.info("Vizier fixture listening on %s", server.endpoint)
    wait_for_termination_signal()
    # DefaultVizierServer exposes no stop method; _server is its started grpc.Server.
    server._server.stop(_SHUTDOWN_GRACE_SECONDS).wait()


if __name__ == "__main__":
    main()
