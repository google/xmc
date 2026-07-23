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

"""Zero-dependency HTTP client with connection recycling for XMC Metrics."""

import enum
import http.client
import random
import threading
import time
import urllib.parse
from typing import Any


class SendResult(enum.Enum):
    """Result status for HTTP metric send operations."""

    SUCCESS = "SUCCESS"
    INVALID_PAYLOAD = "INVALID_PAYLOAD"
    TRANSIENT_FAILURE = "TRANSIENT_FAILURE"


_DEFAULT_TIMEOUT: float = 12.0
_MAX_REQUESTS_PER_CONNECTION: int = 1000
_BASE_TTL_SECONDS: float = 600.0
_JITTER_RANGE_SECONDS: float = 60.0


class HTTPClient:
    """Zero-dependency HTTP client with persistent connection recycling.

    Uses http.client.HTTPConnection or HTTPSConnection from standard library.
    Recycles connection after 1,000 flushes or 10 min ± 60s jitter, or on error.

    Attributes:
        timeout: Request timeout in seconds (default 12.0s).
        max_requests: Maximum requests per persistent connection before recycling.
        base_ttl: Base TTL in seconds for connection lifetime before recycling.
        jitter: Jitter range in seconds applied to connection TTL.
    """

    def __init__(
        self,
        endpoint: str,
        timeout: float = _DEFAULT_TIMEOUT,
        max_requests: int = _MAX_REQUESTS_PER_CONNECTION,
        base_ttl: float = _BASE_TTL_SECONDS,
        jitter: float = _JITTER_RANGE_SECONDS,
        default_headers: dict[str, str] | None = None,
    ) -> None:
        if not endpoint:
            raise ValueError("endpoint must be non-empty.")

        self._parsed_url = urllib.parse.urlparse(endpoint)
        if self._parsed_url.scheme not in ("http", "https"):
            raise ValueError(
                f"Unsupported scheme {self._parsed_url.scheme!r} in endpoint {endpoint!r}"
            )

        self._host = self._parsed_url.hostname or ""
        self._port = self._parsed_url.port
        self._scheme = self._parsed_url.scheme
        self._base_path = self._parsed_url.path.rstrip("/")
        self.timeout = timeout
        self.max_requests = max_requests
        self.base_ttl = base_ttl
        self.jitter = jitter

        self._headers: dict[str, str] = {
            "Connection": "keep-alive",
            "Content-Type": "application/json",
            "User-Agent": "xmc-python-network/0.1.0",
        }
        if default_headers:
            self._headers.update(default_headers)

        self._conn: http.client.HTTPConnection | http.client.HTTPSConnection | None = None
        self._request_count: int = 0
        self._created_at: float = 0.0
        self._current_ttl: float = 0.0
        self._lock = threading.Lock()

    def _calculate_ttl(self) -> float:
        """Calculates connection TTL with random jitter (base_ttl ± jitter)."""
        offset = random.uniform(-self.jitter, self.jitter)
        return max(1.0, self.base_ttl + offset)

    def _connect(self) -> None:
        """Establishes a new HTTP/HTTPS connection."""
        self._close_conn()
        if self._scheme == "https":
            self._conn = http.client.HTTPSConnection(
                self._host, port=self._port, timeout=self.timeout
            )
        else:
            self._conn = http.client.HTTPConnection(
                self._host, port=self._port, timeout=self.timeout
            )
        self._request_count = 0
        self._created_at = time.monotonic()
        self._current_ttl = self._calculate_ttl()

    def _should_recycle(self) -> bool:
        """Determines if the connection should be recycled based on count or age."""
        if self._conn is None:
            return True
        if self._request_count >= self.max_requests:
            return True
        age = time.monotonic() - self._created_at
        return age >= self._current_ttl

    def _ensure_connection(
        self,
    ) -> http.client.HTTPConnection | http.client.HTTPSConnection:
        """Ensures an active, non-expired connection is ready."""
        if self._should_recycle():
            self._connect()
        assert self._conn is not None
        return self._conn

    def send(
        self,
        body: str | bytes,
        path: str = "",
        headers: dict[str, str] | None = None,
        method: str = "POST",
    ) -> SendResult:
        """Sends a payload to the given API path.

        Args:
            body: Request body (bytes or str).
            path: Optional target endpoint subpath.
            headers: Additional per-request headers.
            method: HTTP method (defaults to POST).

        Returns:
            SendResult enum indicating outcome (SUCCESS, INVALID_PAYLOAD, TRANSIENT_FAILURE).
        """
        # Preserve endpoint path (e.g. /v1/metrics) so send() requires no hardcoded subpaths.
        full_path = f"{self._base_path}/{path.lstrip('/')}" if path else self._base_path or "/"
        req_headers = dict(self._headers)
        if headers:
            req_headers.update(headers)

        encoded_body = body.encode("utf-8") if isinstance(body, str) else body

        with self._lock:
            try:
                conn = self._ensure_connection()
                try:
                    conn.request(method.upper(), full_path, body=encoded_body, headers=req_headers)
                    response = conn.getresponse()
                except TimeoutError:
                    raise
                except (http.client.HTTPException, OSError):
                    # Connection error (e.g. stale keep-alive connection closed by server).
                    # Close stale connection and retry once.
                    self._close_conn()
                    conn = self._ensure_connection()
                    conn.request(method.upper(), full_path, body=encoded_body, headers=req_headers)
                    response = conn.getresponse()

                response.read()
                self._request_count += 1
                return self._status_to_send_result(response.status)

            except TimeoutError:
                self._close_conn()
                return SendResult.TRANSIENT_FAILURE

            except Exception:
                self._close_conn()
                return SendResult.TRANSIENT_FAILURE

    def _status_to_send_result(self, status: int) -> SendResult:
        """Maps HTTP status code to SendResult enum."""
        if 200 <= status < 300:
            return SendResult.SUCCESS
        if 400 <= status < 500:
            return SendResult.INVALID_PAYLOAD
        return SendResult.TRANSIENT_FAILURE

    def close(self) -> None:
        """Closes the underlying network connection."""
        with self._lock:
            self._close_conn()

    def _close_conn(self) -> None:
        if self._conn is not None:
            try:
                self._conn.close()
            except Exception:
                pass
            self._conn = None

    def __enter__(self) -> "HTTPClient":
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        self.close()
