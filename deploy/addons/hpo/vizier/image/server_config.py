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

"""Resolves the Vizier server configuration from its environment.

Kept free of google-vizier and SQLAlchemy imports so the resolution rules can be unit tested
without the server's runtime dependencies. The launcher in vizier_server.py turns the result into
a SQLAlchemy engine and a gRPC server.
"""

import dataclasses
from collections.abc import Mapping
from typing import Any
from urllib import parse

DEFAULT_PORT = 7077
DEFAULT_DATABASE_URL = "sqlite:////data/vizier.db"
DEFAULT_POSTGRES_PORT = 5432
DEFAULT_POSTGRES_SSLMODE = "verify-full"

# Prefix of the upstream SQLDataStore columns that hold serialized protos.
SERIALIZED_COLUMN_PREFIX = "serialized_"

# libpq sslmodes that verify the server certificate. They trust the image's system CA bundle,
# matching how the XMC services verify the same PostgreSQL server.
_VERIFYING_SSLMODES = frozenset({"verify-ca", "verify-full"})

# libpq TCP keepalives. The upstream SQL datastore holds one connection for the life of the
# process; keepalives make a silently dropped connection fail fast instead of hanging. Nothing
# reconnects it: the failure surfaces as RPC errors.
_POSTGRES_KEEPALIVES = {
    "keepalives": 1,
    "keepalives_idle": 30,
    "keepalives_interval": 10,
    "keepalives_count": 5,
}

_TRUE_VALUES = frozenset({"1", "true", "yes"})
_FALSE_VALUES = frozenset({"", "0", "false", "no"})


@dataclasses.dataclass(frozen=True)
class ServerConfig:
    """Configuration of the Vizier server process.

    Attributes:
        port: TCP port the gRPC server listens on.
        database_url: SQLAlchemy URL of the database that stores studies and trials.
        enable_reflection: Whether to serve the gRPC server reflection service.
    """

    port: int
    database_url: str
    enable_reflection: bool


@dataclasses.dataclass(frozen=True)
class EngineOptions:
    """Backend-specific options for sqlalchemy.create_engine.

    Attributes:
        connect_args: Keyword arguments passed to the DBAPI connect call.
        static_pool: Whether to share one connection through sqlalchemy.pool.StaticPool.
        isolation_level: Engine isolation level, or None for the driver default.
    """

    connect_args: dict[str, Any]
    static_pool: bool
    isolation_level: str | None


def load_server_config(env: Mapping[str, str]) -> ServerConfig:
    """Builds the server configuration from environment variables.

    Args:
        env: Environment variables, typically os.environ.

    Returns:
        The resolved configuration.

    Raises:
        ValueError: A variable is malformed or a required PostgreSQL variable is missing.
    """
    return ServerConfig(
        port=_parse_port("VIZIER_PORT", env.get("VIZIER_PORT", ""), DEFAULT_PORT),
        database_url=resolve_database_url(env),
        enable_reflection=_parse_bool(
            "VIZIER_ENABLE_REFLECTION", env.get("VIZIER_ENABLE_REFLECTION", "")
        ),
    )


def resolve_database_url(env: Mapping[str, str]) -> str:
    """Returns the SQLAlchemy database URL selected by the environment.

    VIZIER_DATABASE_URL wins when set. Otherwise VIZIER_POSTGRES_HOST selects a PostgreSQL URL
    assembled from the VIZIER_POSTGRES_* variables, and without either the server uses SQLite at
    DEFAULT_DATABASE_URL.

    Args:
        env: Environment variables, typically os.environ.

    Returns:
        A SQLAlchemy database URL.

    Raises:
        ValueError: A required PostgreSQL variable is missing or malformed.
    """
    explicit_url = env.get("VIZIER_DATABASE_URL", "").strip()
    if explicit_url:
        return explicit_url
    if env.get("VIZIER_POSTGRES_HOST", "").strip():
        return build_postgres_url(env)
    return DEFAULT_DATABASE_URL


def build_postgres_url(env: Mapping[str, str]) -> str:
    """Assembles a psycopg2 URL from the VIZIER_POSTGRES_* variables.

    The user, password and database name are percent-encoded, so passwords taken verbatim from a
    Kubernetes Secret may contain URL-reserved characters.

    Args:
        env: Environment variables holding VIZIER_POSTGRES_HOST, VIZIER_POSTGRES_USER and the
            optional VIZIER_POSTGRES_PASSWORD, VIZIER_POSTGRES_PORT, VIZIER_POSTGRES_DATABASE
            and VIZIER_POSTGRES_SSLMODE.

    Returns:
        A postgresql+psycopg2 SQLAlchemy URL.

    Raises:
        ValueError: The host or user is missing, or the port is malformed.
    """
    host = _required(env, "VIZIER_POSTGRES_HOST")
    user = _required(env, "VIZIER_POSTGRES_USER")
    port = _parse_port(
        "VIZIER_POSTGRES_PORT", env.get("VIZIER_POSTGRES_PORT", ""), DEFAULT_POSTGRES_PORT
    )
    database = env.get("VIZIER_POSTGRES_DATABASE", "").strip() or "vizier"
    sslmode = env.get("VIZIER_POSTGRES_SSLMODE", "").strip() or DEFAULT_POSTGRES_SSLMODE
    query = {"sslmode": sslmode}
    if sslmode in _VERIFYING_SSLMODES:
        query["sslrootcert"] = "system"
    credentials = f"{_quote(user)}:{_quote(env.get('VIZIER_POSTGRES_PASSWORD', ''))}"
    netloc = f"{credentials}@{_bracket_ipv6(host)}:{port}"
    return f"postgresql+psycopg2://{netloc}/{_quote(database)}?{parse.urlencode(query)}"


def engine_options(database_url: str) -> EngineOptions:
    """Returns the engine options suited to the database backend of database_url.

    SQLite shares a single connection across the gRPC worker threads, as upstream
    DefaultVizierServer does. PostgreSQL runs in AUTOCOMMIT: the upstream datastore keeps one
    connection for the life of the process and never commits reads, so a transactional session
    would sit "idle in transaction" and stay aborted after a failed read. Upstream's
    check_same_thread argument is SQLite-only and psycopg2 rejects it.

    Args:
        database_url: SQLAlchemy database URL.

    Returns:
        The options for sqlalchemy.create_engine.

    Raises:
        ValueError: The URL names a backend other than SQLite or PostgreSQL.
    """
    backend = database_backend(database_url)
    if backend == "sqlite":
        return EngineOptions(
            connect_args={"check_same_thread": False}, static_pool=True, isolation_level=None
        )
    if backend == "postgresql":
        return EngineOptions(
            connect_args=dict(_POSTGRES_KEEPALIVES),
            static_pool=False,
            isolation_level="AUTOCOMMIT",
        )
    raise ValueError(f"unsupported database backend {backend!r}: use sqlite or postgresql")


def database_backend(database_url: str) -> str:
    """Returns the backend of a SQLAlchemy URL without its driver, e.g. "postgresql"."""
    return parse.urlsplit(database_url).scheme.split("+", 1)[0].lower()


def describe_database(database_url: str) -> str:
    """Returns a log-safe description of database_url that omits credentials and query."""
    parts = parse.urlsplit(database_url)
    host = _bracket_ipv6(parts.hostname) if parts.hostname else ""
    try:
        port = f":{parts.port}" if parts.port else ""
    except ValueError:
        port = ":<invalid port>"
    return f"{parts.scheme}://{host}{port}{parts.path}"


def is_serialized_column(column_name: str) -> bool:
    """Returns whether an upstream datastore column holds a serialized proto."""
    return column_name.startswith(SERIALIZED_COLUMN_PREFIX)


def non_binary_serialized_columns(table_columns: Mapping[str, Mapping[str, str]]) -> list[str]:
    """Returns the serialized-proto columns whose database type is not BYTEA.

    Args:
        table_columns: Column type names (as reported by the database) by column name, by table
            name.

    Returns:
        "table.column (TYPE)" for every offending column, sorted.
    """
    return sorted(
        f"{table}.{column} ({type_name})"
        for table, columns in table_columns.items()
        for column, type_name in columns.items()
        if is_serialized_column(column) and type_name.upper() != "BYTEA"
    )


def _required(env: Mapping[str, str], name: str) -> str:
    """Returns the stripped value of env[name], raising ValueError when it is unset or blank."""
    value = env.get(name, "").strip()
    if not value:
        raise ValueError(f"{name} is required when VIZIER_POSTGRES_HOST is set")
    return value


def _parse_port(name: str, raw: str, default: int) -> int:
    """Parses a TCP port, returning default when raw is blank."""
    if not raw.strip():
        return default
    try:
        port = int(raw)
    except ValueError as error:
        raise ValueError(f"{name} must be an integer, got {raw!r}") from error
    if not 1 <= port <= 65535:
        raise ValueError(f"{name} must be between 1 and 65535, got {port}")
    return port


def _parse_bool(name: str, raw: str) -> bool:
    """Parses a boolean flag, treating blank as false."""
    value = raw.strip().lower()
    if value in _TRUE_VALUES:
        return True
    if value in _FALSE_VALUES:
        return False
    raise ValueError(f"{name} must be true or false, got {raw!r}")


def _quote(value: str) -> str:
    """Percent-encodes every URL-reserved character of value."""
    return parse.quote(value, safe="")


def _bracket_ipv6(host: str) -> str:
    """Wraps an IPv6 literal in brackets so it can be followed by a port."""
    return f"[{host}]" if ":" in host else host
