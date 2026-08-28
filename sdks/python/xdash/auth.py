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
"""Tells the Dashboard Service which human is behind a call made by a service account.

A dashboard's owner is also its ACL principal, and the server derives it from the caller's
identity: the x-goog-user-email metadata key if the call carries one, and otherwise the email
claim of the bearer token. Launchers reach the service with a token minted for an impersonated
service account, so that claim names the service account, and without the header every dashboard
a script creates ends up owned -- and administrable -- by the script's credentials rather than by
the person who ran it. The XManager client solves the same problem the same way; see
AuditMetadataInterceptor in cmd/experiment_state_server/experiment_state_api.py.

The header is asserted by the client and is not checked against the bearer token, so it conveys
who to attribute the call to, not proof of who called. That is the existing contract on the
experiment path, and this module deliberately matches it rather than inventing a second one.
"""

from __future__ import annotations

import logging
import os
from typing import Any, NamedTuple

try:
    import grpc
except ImportError:
    grpc = None  # type: ignore[assignment]

USER_EMAIL_METADATA_KEY = "x-goog-user-email"

# Names the user explicitly, for the cases the credentials cannot answer for themselves: a run
# under a bare service account, or one on a host with no way to reach the userinfo endpoint.
USER_EMAIL_ENV_VAR = "XMC_USER_EMAIL"

_USERINFO_URL = "https://openidconnect.googleapis.com/v1/userinfo"
_USERINFO_TIMEOUT_SEC = 10
_SCOPES = ["openid", "https://www.googleapis.com/auth/userinfo.email"]

_logger = logging.getLogger(__name__)


class _GoogleAuth(NamedTuple):
    """The google-auth entry points identity resolution needs, imported on demand."""

    default: Any
    request_class: Any
    authorized_session_class: Any
    verify_oauth2_token: Any


def _load_google_auth() -> _GoogleAuth | None:
    """Imports google-auth, or returns None when it is not installed.

    Kept out of the module's imports on purpose: the core wheel depends on grpcio and protobuf
    only, and a client talking to a local plaintext service has no use for Google credentials.
    Tests patch this rather than the modules themselves, so the suite stays hermetic.
    """
    try:
        import google.auth
        from google.auth.transport import requests as google_requests
        from google.oauth2 import id_token
    except ImportError:
        return None
    return _GoogleAuth(
        default=google.auth.default,
        request_class=google_requests.Request,
        authorized_session_class=google_requests.AuthorizedSession,
        verify_oauth2_token=id_token.verify_oauth2_token,
    )


def _service_account_email(credentials: Any) -> str | None:
    """Returns the service account the credentials belong to, if they are a service account."""
    return getattr(credentials, "service_account_email", None) or None


def _id_token_email(google_auth: _GoogleAuth, credentials: Any, request: Any) -> str | None:
    """Returns the email claim of the credentials' ID token, if they carry a valid one."""
    raw_token = getattr(credentials, "id_token", None)
    if not raw_token:
        return None
    try:
        claims = google_auth.verify_oauth2_token(raw_token, request)
    except ValueError:
        # The token may have been minted for an audience this process cannot verify. Reading the
        # claim anyway would mean trusting an unverified payload to name the dashboard's owner,
        # so the userinfo endpoint below answers instead.
        return None
    return claims.get("email") or None


def _userinfo_email(google_auth: _GoogleAuth, credentials: Any) -> str | None:
    """Asks the OpenID Connect userinfo endpoint who the credentials belong to."""
    with google_auth.authorized_session_class(credentials) as session:
        response = session.get(_USERINFO_URL, timeout=_USERINFO_TIMEOUT_SEC)
    if response.status_code != 200:
        _logger.warning(
            "userinfo endpoint returned %s; cannot determine the calling user's email",
            response.status_code,
        )
        return None
    return response.json().get("email") or None


def resolve_user_email() -> str | None:
    """Returns the email behind the ambient application default credentials, or None.

    Mirrors get_current_user_email in the XManager client -- service account email, then the ID
    token's claim, then the userinfo endpoint -- with one difference: it returns None instead of
    raising. Identity here is an attribution detail on a call that is authenticated by other
    means, and failing a dashboard creation outright over it would be the worse trade.
    """
    google_auth = _load_google_auth()
    if google_auth is None:
        _logger.debug("google-auth is not installed; no caller identity to send")
        return None
    try:
        credentials, _ = google_auth.default(scopes=_SCOPES)
        request = google_auth.request_class()
        if not credentials.valid:
            credentials.refresh(request)
        return (
            _service_account_email(credentials)
            or _id_token_email(google_auth, credentials, request)
            or _userinfo_email(google_auth, credentials)
        )
    except Exception as err:  # pylint: disable=broad-except
        # google-auth raises DefaultCredentialsError, RefreshError and bare transport errors from
        # three different packages, and every one of them means the same thing here: nobody to
        # name. Catching them individually would only lengthen the list the next release breaks.
        _logger.warning("Could not resolve the calling user's email: %s", err)
        return None


# Distinguishes "not looked up yet" from a lookup that came back empty, so a failed resolution is
# not retried on every RPC.
_UNRESOLVED = object()

# Subclassing is what makes grpc.intercept_channel accept the object, and the base class only
# exists when grpcio is installed. The ignore matches the one in the XManager client, which
# subclasses the same untyped third-party class under disallow_subclassing_any.
_InterceptorBase: Any = grpc.UnaryUnaryClientInterceptor if grpc is not None else object


class UserEmailInterceptor(_InterceptorBase):  # type: ignore[misc]
    """Adds x-goog-user-email to every unary call, naming the human behind the credentials."""

    def __init__(self, user_email: str | None = None) -> None:
        """Prepares the interceptor.

        Args:
            user_email: The email to send. Left unset, it is resolved from XMC_USER_EMAIL and
                then from the ambient credentials, on the first call rather than here:
                constructing a client should not reach out to the metadata server or the
                userinfo endpoint, and a client that is never used should cost nothing.
        """
        self._user_email: Any = user_email or _UNRESOLVED

    def _email(self) -> str | None:
        """Returns the email to send, resolving and caching it on first use."""
        if self._user_email is _UNRESOLVED:
            self._user_email = os.environ.get(USER_EMAIL_ENV_VAR) or resolve_user_email()
            if not self._user_email:
                _logger.warning(
                    "No caller identity available: the Dashboard Service will record the "
                    "authenticated principal -- a service account, for an impersonated run -- "
                    "as the owner of anything created. Set %s to name the user explicitly.",
                    USER_EMAIL_ENV_VAR,
                )
        return self._user_email

    def intercept_unary_unary(self, continuation: Any, client_call_details: Any, request: Any):
        """Forwards the call with the caller's email appended to its metadata."""
        email = self._email()
        if not email:
            return continuation(client_call_details, request)
        call_metadata = list(client_call_details.metadata or [])
        # A caller that set the key itself has already said who the call is for. Appending a
        # second copy would leave the server choosing between two contradictory identities, and
        # it takes the first one it finds.
        if any(key.lower() == USER_EMAIL_METADATA_KEY for key, _ in call_metadata):
            return continuation(client_call_details, request)
        call_metadata.append((USER_EMAIL_METADATA_KEY, email))
        return continuation(client_call_details._replace(metadata=call_metadata), request)
