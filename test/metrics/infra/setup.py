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

"""Python testing harness and environment mapping for XMC Metrics infrastructure."""

import dataclasses
import logging
import os
import socket
import subprocess
from collections.abc import Iterator, Mapping
from contextlib import contextmanager

logger = logging.getLogger(__name__)

DEFAULT_REDPANDA_HOST = "127.0.0.1"
DEFAULT_REDPANDA_PORT = 9092
DEFAULT_CLICKHOUSE_HOST = "127.0.0.1"
DEFAULT_CLICKHOUSE_NATIVE_PORT = 9000
DEFAULT_CLICKHOUSE_HTTP_PORT = 8123
DEFAULT_GATEWAY_URL = "http://127.0.0.1:8080"


@dataclasses.dataclass(frozen=True)
class Endpoint:
    """Represents a network endpoint host and port pairing."""

    host: str
    port: int

    @property
    def address(self) -> str:
        """Returns the formatted host:port string representation."""
        return f"{self.host}:{self.port}"


def verify_tcp_connectivity(host: str, port: int, timeout: float = 5.0) -> bool:
    """Attempts a TCP connection to verify endpoint connectivity.

    Args:
        host: Target hostname or IP address.
        port: Target TCP port number.
        timeout: Socket connection timeout in seconds.

    Returns:
        True if TCP connection succeeds, False otherwise.
    """
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (OSError, TimeoutError) as err:
        logger.debug("TCP connectivity check to %s:%d failed: %s", host, port, err)
        return False


def parse_endpoint(addr: str | tuple[str, int] | Endpoint, default_port: int) -> Endpoint:
    """Parses various address representations into a normalized Endpoint.

    Args:
        addr: Raw string 'host:port', tuple (host, port), or Endpoint.
        default_port: Default port if string omits port.

    Returns:
        Normalized Endpoint object.
    """
    if isinstance(addr, Endpoint):
        ep = addr
    elif isinstance(addr, tuple):
        ep = Endpoint(host=addr[0], port=int(addr[1]))
    elif ":" in addr:
        parts = addr.rsplit(":", 1)
        ep = Endpoint(host=parts[0], port=int(parts[1]))
    else:
        ep = Endpoint(host=addr, port=default_port)

    if ep.host in ("0.0.0.0", "::", "[::]", "localhost"):
        return Endpoint(host="127.0.0.1", port=ep.port)
    return ep


def resolve_container_endpoint(
    container_name: str,
    internal_port: int,
    default_host: str = "127.0.0.1",
    default_port: int | None = None,
) -> Endpoint:
    """Resolves container host port via Docker CLI if available.

    Args:
        container_name: Docker container name or ID.
        internal_port: Internal container port number.
        default_host: Fallback host IP address.
        default_port: Fallback host port number.

    Returns:
        Resolved Endpoint or fallback defaults.
    """
    fallback_port = default_port if default_port is not None else internal_port
    try:
        res = subprocess.run(
            ["docker", "port", "--", container_name, f"{internal_port}/tcp"],
            capture_output=True,
            text=True,
            check=False,
            timeout=3.0,
        )
        if res.returncode != 0 or not res.stdout.strip():
            return Endpoint(host=default_host, port=fallback_port)

        first_line = res.stdout.strip().splitlines()[0]
        mapped_addr = first_line.split("->")[-1].strip()
        if ":" not in mapped_addr:
            return Endpoint(host=default_host, port=fallback_port)
        return parse_endpoint(mapped_addr, default_port=fallback_port)
    except (subprocess.SubprocessError, OSError, ValueError, IndexError):
        return Endpoint(host=default_host, port=fallback_port)


class MetricsTestHarness:
    """Manages Python test environment setup and container endpoint resolution."""

    def __init__(
        self,
        redpanda: str | tuple[str, int] | Endpoint = (
            DEFAULT_REDPANDA_HOST,
            DEFAULT_REDPANDA_PORT,
        ),
        clickhouse_native: str | tuple[str, int] | Endpoint = (
            DEFAULT_CLICKHOUSE_HOST,
            DEFAULT_CLICKHOUSE_NATIVE_PORT,
        ),
        clickhouse_http: str | tuple[str, int] | Endpoint = (
            DEFAULT_CLICKHOUSE_HOST,
            DEFAULT_CLICKHOUSE_HTTP_PORT,
        ),
        gateway_url: str = DEFAULT_GATEWAY_URL,
        env_overrides: Mapping[str, str] | None = None,
    ) -> None:
        self.env_overrides = dict(env_overrides) if env_overrides else {}

        redpanda = self.env_overrides.get(
            "XMC_REDPANDA_BROKERS",
            self.env_overrides.get("REDPANDA_BROKERS", redpanda),
        )
        clickhouse_native = self.env_overrides.get(
            "XMC_CLICKHOUSE_ADDR",
            self.env_overrides.get("CLICKHOUSE_ADDR", clickhouse_native),
        )
        clickhouse_http = self.env_overrides.get("XMC_CLICKHOUSE_HTTP_ADDR", clickhouse_http)
        gateway_url = self.env_overrides.get("XMC_GATEWAY_URL", gateway_url)

        self.redpanda = parse_endpoint(redpanda, DEFAULT_REDPANDA_PORT)
        self.clickhouse_native = parse_endpoint(clickhouse_native, DEFAULT_CLICKHOUSE_NATIVE_PORT)
        self.clickhouse_http = parse_endpoint(clickhouse_http, DEFAULT_CLICKHOUSE_HTTP_PORT)
        self.gateway_url = gateway_url
        self._saved_env: dict[str, str | None] = {}

    def get_env_mapping(self) -> dict[str, str]:
        """Constructs environment variable mapping for test execution."""
        env_map = {
            "XMC_REDPANDA_BROKERS": self.redpanda.address,
            "REDPANDA_BROKERS": self.redpanda.address,
            "XMC_CLICKHOUSE_ADDR": self.clickhouse_native.address,
            "CLICKHOUSE_ADDR": self.clickhouse_native.address,
            "XMC_CLICKHOUSE_HTTP_ADDR": self.clickhouse_http.address,
            "XMC_GATEWAY_URL": self.gateway_url,
        }
        env_map.update(self.env_overrides)
        return env_map

    def apply_env(self) -> dict[str, str]:
        """Applies configured environment variables to os.environ."""
        mapping = self.get_env_mapping()
        for key, val in mapping.items():
            if key not in self._saved_env:
                self._saved_env[key] = os.environ.get(key)
            os.environ[key] = val
        return mapping

    def restore_env(self) -> None:
        """Restores os.environ to state before apply_env was called."""
        for key, saved_val in self._saved_env.items():
            if saved_val is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = saved_val
        self._saved_env.clear()

    @contextmanager
    def env_context(self) -> Iterator["MetricsTestHarness"]:
        """Context manager for temporary environment modification."""
        self.apply_env()
        try:
            yield self
        finally:
            self.restore_env()

    def smoke_test(self, timeout: float = 5.0) -> dict[str, bool]:
        """Executes basic TCP connection smoke tests for endpoints."""
        return {
            "redpanda": verify_tcp_connectivity(
                self.redpanda.host, self.redpanda.port, timeout=timeout
            ),
            "clickhouse_native": verify_tcp_connectivity(
                self.clickhouse_native.host,
                self.clickhouse_native.port,
                timeout=timeout,
            ),
            "clickhouse_http": verify_tcp_connectivity(
                self.clickhouse_http.host,
                self.clickhouse_http.port,
                timeout=timeout,
            ),
        }

    def verify_all_endpoints(self, timeout: float = 5.0) -> None:
        """Verifies all endpoints respond to TCP connectivity checks."""
        results = self.smoke_test(timeout=timeout)
        failed = []
        if not results.get("redpanda", False):
            failed.append(f"redpanda ({self.redpanda.address})")
        if not results.get("clickhouse_native", False):
            failed.append(f"clickhouse_native ({self.clickhouse_native.address})")
        if not results.get("clickhouse_http", False):
            failed.append(f"clickhouse_http ({self.clickhouse_http.address})")
        if failed:
            raise ConnectionError(f"Smoke test failed for endpoints: {', '.join(failed)}")


def create_test_harness(
    auto_resolve_containers: bool = True,
    env_overrides: Mapping[str, str] | None = None,
) -> MetricsTestHarness:
    """Factory creating a MetricsTestHarness with optional Docker resolution.

    Args:
        auto_resolve_containers: Query docker port for container endpoints.
        env_overrides: Custom environment variable overrides.

    Returns:
        Configured MetricsTestHarness instance.
    """
    if not auto_resolve_containers:
        return MetricsTestHarness(env_overrides=env_overrides)

    redpanda_ep = resolve_container_endpoint(
        "redpanda", DEFAULT_REDPANDA_PORT, DEFAULT_REDPANDA_HOST
    )
    ch_native_ep = resolve_container_endpoint(
        "clickhouse", DEFAULT_CLICKHOUSE_NATIVE_PORT, DEFAULT_CLICKHOUSE_HOST
    )
    ch_http_ep = resolve_container_endpoint(
        "clickhouse", DEFAULT_CLICKHOUSE_HTTP_PORT, DEFAULT_CLICKHOUSE_HOST
    )

    return MetricsTestHarness(
        redpanda=redpanda_ep,
        clickhouse_native=ch_native_ep,
        clickhouse_http=ch_http_ep,
        env_overrides=env_overrides,
    )
