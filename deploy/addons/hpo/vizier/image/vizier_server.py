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

"""Runs the Open Source Vizier gRPC service on a SQLite or PostgreSQL datastore.

Upstream vizier.service.servers.DefaultVizierServer creates its engine with SQLite-only
arguments (check_same_thread, StaticPool), which psycopg2 rejects, and upstream's SQL schema keeps
serialized protos in VARCHAR columns, which PostgreSQL cannot store. This launcher reproduces the
DefaultVizierServer wiring from google-vizier 0.1.24 instead: a VizierServicer whose datastore is
a SQLDataStore on an engine built for the configured backend (with BYTEA proto columns on
PostgreSQL), an in-process Pythia servicer, and a gRPC server. It restricts studies to the
algorithms the image supports (see supported_algorithms.py) and, when enabled, serves gRPC server
reflection so that grpcurl can call the service without local proto files.

Configuration comes from the environment; see server_config.load_server_config.
"""

import datetime
import importlib.metadata
import logging
import os
import signal
import threading
from concurrent import futures

import grpc
import server_config
import sqlalchemy as sqla
import supported_algorithms
from grpc_reflection.v1alpha import reflection
from vizier import pythia, pyvizier as vz
from vizier._src.service import (
    policy_factory,
    pythia_service,
    sql_datastore,
    study_pb2,
    vizier_service,
    vizier_service_pb2,
    vizier_service_pb2_grpc,
)

# The launcher wires private vizier._src.service internals that are verified only against this
# release. Keep it equal to the google-vizier pin in requirements.in.
_SUPPORTED_VIZIER_VERSION = "0.1.24"

# Mirror DefaultVizierServer in google-vizier 0.1.24.
_MAX_WORKERS = 30
_EARLY_STOP_RECYCLE_PERIOD = datetime.timedelta(seconds=0.1)

# Lets in-flight RPCs finish on SIGTERM, within the default 30s pod termination grace period.
_SHUTDOWN_GRACE_SECONDS = 10.0


# grpc, google-vizier and SQLAlchemy are installed only in the image, so mypy sees their classes as
# Any; the subclasses below carry `type: ignore[misc]` for disallow_subclassing_any.


class UnsupportedAlgorithmRpcError(grpc.RpcError):  # type: ignore[misc]
    """An unsupported algorithm reported as a gRPC error.

    Upstream SuggestTrials turns a grpc.RpcError from Pythia into a done operation carrying the
    error, whereas any other exception escapes the RPC and leaves the operation pending forever.
    """

    def __init__(self, message: str):
        super().__init__(message)
        self._message = message

    def code(self) -> grpc.StatusCode:
        """Returns the gRPC status code of the error."""
        return grpc.StatusCode.INVALID_ARGUMENT

    def details(self) -> str:
        """Returns the error message."""
        return self._message


class RestrictedPolicyFactory(pythia.PolicyFactory):  # type: ignore[misc]
    """Upstream DefaultPolicyFactory limited to supported_algorithms.SUPPORTED_ALGORITHMS."""

    def __init__(self) -> None:
        self._delegate = policy_factory.DefaultPolicyFactory()

    def __call__(
        self,
        problem_statement: vz.ProblemStatement,
        algorithm: str,
        policy_supporter: pythia.PolicySupporter,
        study_name: str,
    ) -> pythia.Policy:
        """Returns the upstream policy for a supported algorithm.

        Raises:
            UnsupportedAlgorithmRpcError: The algorithm is not supported.
        """
        try:
            supported_algorithms.check_algorithm(algorithm)
        except supported_algorithms.UnsupportedAlgorithmError as error:
            raise UnsupportedAlgorithmRpcError(str(error)) from error
        return self._delegate(problem_statement, algorithm, policy_supporter, study_name)


class RestrictedVizierServicer(vizier_service.VizierServicer):  # type: ignore[misc]
    """VizierServicer that rejects studies with unsupported algorithms at creation.

    Rejecting in CreateStudy reports INVALID_ARGUMENT to the client. RestrictedPolicyFactory
    still guards SuggestTrials, where upstream reports every Pythia failure as INTERNAL in the
    returned operation.
    """

    def CreateStudy(  # noqa: N802 - gRPC method name.
        self,
        request: vizier_service_pb2.CreateStudyRequest,
        context: grpc.ServicerContext | None = None,
    ) -> study_pb2.Study:
        """Creates or loads a study after checking its algorithm.

        Args:
            request: The CreateStudy request.
            context: The gRPC context; None only for in-process calls.

        Returns:
            The created or loaded study.

        Raises:
            supported_algorithms.UnsupportedAlgorithmError: The algorithm is not supported and
                there is no gRPC context to abort.
        """
        try:
            supported_algorithms.check_algorithm(request.study.study_spec.algorithm)
        except supported_algorithms.UnsupportedAlgorithmError as error:
            if context is None:
                raise
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
        return super().CreateStudy(request, context)


def check_vizier_version() -> None:
    """Fails unless the installed google-vizier is the release the launcher was verified with.

    Raises:
        RuntimeError: Another google-vizier release is installed.
    """
    installed = importlib.metadata.version("google-vizier")
    if installed != _SUPPORTED_VIZIER_VERSION:
        raise RuntimeError(
            f"google-vizier {installed} is installed, but this launcher supports only "
            f"{_SUPPORTED_VIZIER_VERSION}"
        )


def create_engine(database_url: str) -> sqla.engine.Engine:
    """Creates a SQLAlchemy engine with the options suited to the URL's backend.

    Args:
        database_url: SQLAlchemy database URL.

    Returns:
        The engine.

    Raises:
        ValueError: The URL is malformed or names an unsupported backend. The message never
            includes the URL, which may hold a password.
    """
    options = server_config.engine_options(database_url)
    kwargs = {"connect_args": options.connect_args}
    if options.static_pool:
        kwargs["poolclass"] = sqla.pool.StaticPool
    if options.isolation_level:
        kwargs["isolation_level"] = options.isolation_level
    try:
        return sqla.create_engine(database_url, **kwargs)
    except (sqla.exc.ArgumentError, ValueError):
        raise ValueError("invalid VIZIER_DATABASE_URL") from None


def create_datastore(engine: sqla.engine.Engine) -> sql_datastore.SQLDataStore:
    """Creates the upstream SQL datastore on engine, with BYTEA proto columns on PostgreSQL.

    On PostgreSQL a Column listener retypes the serialized-proto columns to LargeBinary while
    upstream builds its tables, so both the DDL and the statement binds use BYTEA. The listener
    is global to SQLAlchemy, so it is registered only for the single-threaded construction and
    removed right after.

    Args:
        engine: Engine of the database that stores studies and trials.

    Returns:
        The datastore.

    Raises:
        RuntimeError: A serialized-proto column already exists with a type other than BYTEA.
    """
    if engine.dialect.name != "postgresql":
        return sql_datastore.SQLDataStore(engine)
    sqla.event.listen(sqla.Column, "after_parent_attach", _retype_serialized_column)
    try:
        datastore = sql_datastore.SQLDataStore(engine)
    finally:
        sqla.event.remove(sqla.Column, "after_parent_attach", _retype_serialized_column)
    _check_serialized_columns_are_binary(engine)
    return datastore


def _retype_serialized_column(column: sqla.Column, _parent: object) -> None:
    """Switches a serialized-proto column to LargeBinary as it is attached to its table."""
    if server_config.is_serialized_column(column.name):
        column.type = sqla.LargeBinary()


def _check_serialized_columns_are_binary(engine: sqla.engine.Engine) -> None:
    """Fails fast when the live schema keeps serialized protos in non-BYTEA columns.

    create_all never alters existing tables, so tables created earlier with upstream's VARCHAR
    columns would only fail on the first write.

    Raises:
        RuntimeError: A serialized-proto column is not BYTEA.
    """
    inspector = sqla.inspect(engine)
    table_columns = {
        table: {column["name"]: str(column["type"]) for column in inspector.get_columns(table)}
        for table in inspector.get_table_names()
    }
    offending = server_config.non_binary_serialized_columns(table_columns)
    if offending:
        raise RuntimeError(
            f"Vizier needs BYTEA serialized-proto columns, found {', '.join(offending)}; "
            "drop the Vizier tables or migrate these columns to BYTEA"
        )


def create_servicer(datastore: sql_datastore.SQLDataStore) -> vizier_service.VizierServicer:
    """Creates the Vizier servicer on datastore with the restricted in-process Pythia service.

    The servicer starts on the RAM datastore, which is replaced before it serves any request.
    Its default Pythia service is replaced as DefaultVizierServer does, here with
    RestrictedPolicyFactory.

    Args:
        datastore: Datastore of studies and trials.

    Returns:
        The servicer.
    """
    servicer = RestrictedVizierServicer(
        database_url=None, early_stop_recycle_period=_EARLY_STOP_RECYCLE_PERIOD
    )
    servicer.datastore = datastore
    servicer.default_pythia_service = pythia_service.PythiaServicer(
        servicer, policy_factory=RestrictedPolicyFactory()
    )
    return servicer


def create_server(
    servicer: vizier_service.VizierServicer, port: int, enable_reflection: bool
) -> grpc.Server:
    """Creates an unstarted gRPC server exposing servicer on port.

    Args:
        servicer: The Vizier servicer.
        port: TCP port to listen on, on all interfaces.
        enable_reflection: Whether to also serve gRPC server reflection.

    Returns:
        The server.

    Raises:
        RuntimeError: The port cannot be bound.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=_MAX_WORKERS))
    vizier_service_pb2_grpc.add_VizierServiceServicer_to_server(servicer, server)
    if enable_reflection:
        service_names = (
            vizier_service_pb2.DESCRIPTOR.services_by_name["VizierService"].full_name,
            reflection.SERVICE_NAME,
        )
        reflection.enable_server_reflection(service_names, server)
    if server.add_insecure_port(f"[::]:{port}") == 0:
        raise RuntimeError(f"failed to bind gRPC port {port}")
    return server


def serve_until_terminated(server: grpc.Server) -> None:
    """Starts server and blocks until SIGTERM or SIGINT, then stops it gracefully.

    Args:
        server: An unstarted gRPC server.
    """
    stop_requested = threading.Event()

    def request_stop(signum: int, _frame: object) -> None:
        logging.info("Received signal %d, shutting down", signum)
        stop_requested.set()

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    server.start()
    stop_requested.wait()
    server.stop(_SHUTDOWN_GRACE_SECONDS).wait()


def main() -> None:
    """Runs the Vizier server configured by the environment."""
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    check_vizier_version()
    config = server_config.load_server_config(os.environ)
    logging.info("Using database %s", server_config.describe_database(config.database_url))
    engine = create_engine(config.database_url)
    try:
        servicer = create_servicer(create_datastore(engine))
        server = create_server(servicer, config.port, config.enable_reflection)
        logging.info(
            "Vizier server listening on port %d (reflection %s)",
            config.port,
            "on" if config.enable_reflection else "off",
        )
        serve_until_terminated(server)
    finally:
        engine.dispose()


if __name__ == "__main__":
    main()
