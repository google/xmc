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
import socket
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, Optional, Union


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

    Attributes:
        base_url: Endpoint base URL (e.g. 'https://xmc.googleapis.com').
        api_key: Optional API key or bearer token.
        timeout: Default request timeout in seconds.
    """

    def __init__(
        self,
        base_url: str,
        api_key: Optional[str] = None,
        timeout: float = 30.0,
        default_headers: Optional[Dict[str, str]] = None,
    ) -> None:
        if not base_url:
            raise ValueError("base_url must be specified and non-empty.")

        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self._headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": "xmc-python-sdk/0.1.0",
        }
        if default_headers:
            self._headers.update(default_headers)
        if api_key:
            self._headers["Authorization"] = f"Bearer {api_key}"

    def request(
        self,
        method: str,
        path: str,
        params: Optional[Dict[str, Any]] = None,
        data: Optional[Union[Dict[str, Any], list]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Executes an HTTP request using urllib.request.

        Args:
            method: HTTP verb ('GET', 'POST', 'PUT', 'DELETE', etc.).
            path: Relative API endpoint path.
            params: Optional URL query parameter key-value pairs.
            data: Optional request payload serializable to JSON.
            headers: Additional request headers.

        Returns:
            Parsed JSON payload as a dictionary/list, or empty dictionary for 204.

        Raises:
            XMCAPIError: If remote server returns an HTTP error status.
            XMCNetworkError: If a connection error or timeout occurs.
            XMCError: If response body cannot be parsed as valid JSON.
        """
        url = f"{self.base_url}/{path.lstrip('/')}"
        if params:
            query_string = urllib.parse.urlencode(params)
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
            if isinstance(err.reason, socket.timeout):
                raise XMCNetworkError("Request timed out", original_error=err) from err
            raise XMCNetworkError(f"Network error: {err.reason}", original_error=err) from err
        except socket.timeout as err:
            raise XMCNetworkError("Request timed out", original_error=err) from err
        except json.JSONDecodeError as err:
            raise XMCError(f"Failed to parse API JSON response: {err}") from err

    def get(
        self,
        path: str,
        params: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Convenience method for HTTP GET requests."""
        return self.request("GET", path, params=params, headers=headers)

    def post(
        self,
        path: str,
        data: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        """Convenience method for HTTP POST requests."""
        return self.request("POST", path, data=data, headers=headers)

    def close(self) -> None:
        """Closes any underlying network resources."""
        pass

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        self.close()
