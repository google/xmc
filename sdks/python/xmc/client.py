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

"""Base HTTP client for XMC Metrics API using Python standard library."""

import json
import logging
import os
import socket
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Mapping, Optional, Union

from xmc.dist_harvest import get_replica_id_and_silence_status

logger = logging.getLogger(__name__)

_TRUTHY_ENV_VALUES = frozenset({"1", "true", "yes", "t", "y"})


def _is_metrics_disabled_env() -> bool:
    """Checks whether XMC_METRICS_DISABLED env var is set to a truthy value."""
    val = os.getenv("XMC_METRICS_DISABLED", "").strip().lower()
    return val in _TRUTHY_ENV_VALUES


class XMCError(Exception):
    """Base exception class for all XMC SDK errors."""


class XMCNetworkError(XMCError):
    """Raised when a network failure or timeout occurs during API calls."""

    def __init__(self, message: str, original_error: Optional[Exception] = None) -> None:
        super().__init__(message)
        self.original_error = original_error


class XMCAPIError(XMCError):
    """Raised when the XMC API returns an HTTP 4xx/5xx status code."""

    def __init__(self, status_code: int, message: str, response_body: Optional[str] = None) -> None:
        super().__init__(f"HTTP {status_code}: {message}")
        self.status_code = status_code
        self.response_body = response_body


class Client:
    """Base HTTP client for interacting with XMC Metrics APIs.

    Note:
        Uses urllib.request.urlopen for zero external runtime dependencies.
        TODO: Higher-level SDK producers should implement request
        batching and connection reuse to avoid per-request TLS overhead in
        high-frequency logging loops.

    Attributes:
        base_url: Endpoint base URL (e.g. 'https://xmc.googleapis.com').
        api_key: Optional API key or bearer token.
        timeout: Default request timeout in seconds.
        disabled: Whether metrics operations are disabled/short-circuited.
        log_all_replicas: Opt-in to bypass rank-zero silencer.
        replica_id: Auto-detected replica/rank ID.
        is_silenced: Whether metric logging is silenced for this replica.
        should_silence: Alias for is_silenced.
    """

    def __init__(
        self,
        base_url: str,
        api_key: Optional[str] = None,
        timeout: float = 30.0,
        default_headers: Optional[Dict[str, str]] = None,
        disabled: Optional[bool] = None,
        log_all_replicas: bool = False,
        env: Optional[Mapping[str, str]] = None,
    ) -> None:
        if not base_url:
            raise ValueError("base_url must be specified and non-empty.")

        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        if disabled is not None:
            self._disabled = bool(disabled)
        else:
            self._disabled = _is_metrics_disabled_env()

        self.log_all_replicas = log_all_replicas
        self.replica_id, self.is_silenced = get_replica_id_and_silence_status(
            env=env, log_all_replicas=log_all_replicas
        )
        self.should_silence = self.is_silenced

        self._headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "xmc-python-sdk/0.1.0",
        }
        if self.replica_id:
            self._headers["X-Replica-ID"] = self.replica_id
        if default_headers:
            self._headers.update(default_headers)
        if api_key:
            self._headers["Authorization"] = f"Bearer {api_key}"

    @property
    def disabled(self) -> bool:
        """Returns whether metrics operations are short-circuited/disabled."""
        return self._disabled

    def request(
        self,
        method: str,
        path: str,
        params: Optional[Dict[str, Any]] = None,
        data: Optional[Union[Dict[str, Any], list]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Union[Dict[str, Any], list]:
        """Executes an HTTP request using urllib.request.

        Args:
            method: HTTP verb ('GET', 'POST', 'PUT', 'DELETE', etc.).
            path: Relative API endpoint path.
            params: Optional URL query parameter key-value pairs.
            data: Optional request payload serializable to JSON.
            headers: Additional request headers.

        Returns:
            Parsed JSON payload as a dictionary or list, or empty dictionary for 204.

        Raises:
            XMCAPIError: If remote server returns an HTTP error status.
            XMCNetworkError: If a connection error or timeout occurs.
            XMCError: If response body cannot be parsed as valid JSON.
        """
        if self.disabled:
            return {}

        url = f"{self.base_url}/{path.lstrip('/')}"
        if params:
            query_string = urllib.parse.urlencode(params, doseq=True)
            url = f"{url}?{query_string}"

        req_headers = dict(self._headers)
        if headers:
            req_headers.update(headers)

        body = None
        if data is not None:
            body = json.dumps(data).encode("utf-8")

        req = urllib.request.Request(
            url=url,
            data=body,
            headers=req_headers,
            method=method.upper(),
        )

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as response:
                raw_bytes = response.read()
                if not raw_bytes:
                    return {}
                return json.loads(raw_bytes.decode("utf-8"))
        except urllib.error.HTTPError as err:
            err_body = err.read().decode("utf-8", errors="replace")
            raise XMCAPIError(
                status_code=err.code,
                message=err.reason,
                response_body=err_body,
            ) from err
        except urllib.error.URLError as err:
            if isinstance(err.reason, (socket.timeout, TimeoutError)):
                raise XMCNetworkError("Request timed out", original_error=err) from err
            raise XMCNetworkError(f"Network error: {err.reason}", original_error=err) from err
        except (socket.timeout, TimeoutError) as err:
            raise XMCNetworkError("Request timed out", original_error=err) from err
        except json.JSONDecodeError as err:
            raise XMCError(f"Failed to parse API JSON response: {err}") from err

    def get(
        self,
        path: str,
        params: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Union[Dict[str, Any], list]:
        """Convenience method for HTTP GET requests."""
        return self.request("GET", path, params=params, headers=headers)

    def post(
        self,
        path: str,
        data: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Union[Dict[str, Any], list]:
        """Convenience method for HTTP POST requests."""
        if self.is_silenced:
            logger.debug("POST request to %s silenced for replica %s", path, self.replica_id)
            return {}
        return self.request("POST", path, data=data, headers=headers)

    def close(self) -> None:
        """Closes any underlying network resources."""
        pass

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        self.close()
