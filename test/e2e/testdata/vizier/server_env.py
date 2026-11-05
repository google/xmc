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

"""Reads the dev/test Vizier fixture settings from its environment.

Kept free of google-vizier imports so it can be unit tested without them.
"""

import dataclasses
from collections.abc import Mapping

DEFAULT_PORT = 7077
DEFAULT_DATABASE_URL = "sqlite:////data/vizier.db"


@dataclasses.dataclass(frozen=True)
class ServerEnv:
    """Settings of the Vizier fixture process.

    Attributes:
        port: TCP port the gRPC server listens on.
        database_url: SQLAlchemy URL of the database that stores studies and trials.
    """

    port: int
    database_url: str


def load_server_env(env: Mapping[str, str]) -> ServerEnv:
    """Reads VIZIER_PORT and VIZIER_DATABASE_URL, defaulting blank values.

    Args:
        env: Environment variables, typically os.environ.

    Returns:
        The settings.

    Raises:
        ValueError: VIZIER_PORT is not an integer between 1 and 65535.
    """
    return ServerEnv(
        port=parse_port(env.get("VIZIER_PORT", "")),
        database_url=env.get("VIZIER_DATABASE_URL", "").strip() or DEFAULT_DATABASE_URL,
    )


def parse_port(raw: str) -> int:
    """Parses VIZIER_PORT, returning DEFAULT_PORT when raw is blank.

    Raises:
        ValueError: raw is not an integer between 1 and 65535.
    """
    if not raw.strip():
        return DEFAULT_PORT
    # int() also accepts non-ASCII digits, which no port setting should contain.
    if not raw.isascii():
        raise ValueError(f"VIZIER_PORT must be an integer, got {raw!r}")
    try:
        port = int(raw)
    except ValueError:
        raise ValueError(f"VIZIER_PORT must be an integer, got {raw!r}") from None
    if not 1 <= port <= 65535:
        raise ValueError(f"VIZIER_PORT must be between 1 and 65535, got {port}")
    return port
