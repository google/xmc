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
"""Unit tests for the caller identity xdash attaches to Dashboard Service calls."""

import os
import unittest
from typing import Any
from unittest import mock

from xdash.auth import (
    USER_EMAIL_ENV_VAR,
    USER_EMAIL_METADATA_KEY,
    UserEmailInterceptor,
    _GoogleAuth,
    resolve_user_email,
)


class FakeResponse:
    """Stands in for the userinfo endpoint's HTTP response."""

    def __init__(self, status_code: int = 200, payload: dict[str, Any] | None = None) -> None:
        self.status_code = status_code
        self._payload = payload or {}

    def json(self) -> dict[str, Any]:
        return self._payload


class FakeSession:
    """An AuthorizedSession whose get() always answers with the response it was built on."""

    def __init__(self, response: FakeResponse) -> None:
        self._response = response
        self.closed = False

    def __enter__(self) -> "FakeSession":
        return self

    def __exit__(self, *_: Any) -> None:
        self.closed = True

    def get(self, url: str, timeout: int | None = None) -> FakeResponse:
        del url, timeout
        return self._response


def fake_google_auth(
    credentials: Any,
    userinfo: FakeResponse | None = None,
    verify: Any = None,
    sessions: list[FakeSession] | None = None,
) -> _GoogleAuth:
    """Builds the google-auth surface resolve_user_email uses, with no real package behind it.

    Args:
        credentials: The credentials google.auth.default should hand back.
        userinfo: The response the userinfo endpoint should answer with.
        verify: Stands in for id_token.verify_oauth2_token.
        sessions: Collects each AuthorizedSession opened, for a test asserting it was released.
    """

    def authorized_session(_: Any) -> FakeSession:
        session = FakeSession(userinfo or FakeResponse(payload={}))
        if sessions is not None:
            sessions.append(session)
        return session

    return _GoogleAuth(
        default=lambda scopes: (credentials, "project"),
        request_class=lambda: "request",
        authorized_session_class=authorized_session,
        verify_oauth2_token=verify or (lambda *_: {}),
    )


class FakeCredentials:
    """Application default credentials with only the attributes the resolver reads."""

    def __init__(self, valid: bool = True, **attributes: Any) -> None:
        self.valid = valid
        self.refreshed = False
        for name, value in attributes.items():
            setattr(self, name, value)

    def refresh(self, request: Any) -> None:
        del request
        self.refreshed = True
        self.valid = True


class ResolveUserEmailTest(unittest.TestCase):
    """Covers the ladder that turns ambient credentials into an email, and its failure modes."""

    def _resolve_with(self, google_auth: Any) -> str | None:
        with mock.patch("xdash.auth._load_google_auth", return_value=google_auth):
            return resolve_user_email()

    def test_returns_none_without_google_auth(self) -> None:
        """The core wheel does not depend on google-auth, so its absence is not an error."""
        self.assertIsNone(self._resolve_with(None))

    def test_prefers_the_service_account_email(self) -> None:
        credentials = FakeCredentials(service_account_email="sa@example.iam.gserviceaccount.com")

        email = self._resolve_with(fake_google_auth(credentials))

        self.assertEqual(email, "sa@example.iam.gserviceaccount.com")

    def test_falls_back_to_the_id_token_claim(self) -> None:
        credentials = FakeCredentials(id_token="a.b.c")

        email = self._resolve_with(
            fake_google_auth(credentials, verify=lambda *_: {"email": "user@example.com"})
        )

        self.assertEqual(email, "user@example.com")

    def test_unverifiable_id_token_falls_through_to_userinfo(self) -> None:
        """An ID token minted for another audience must not be trusted to name the owner."""

        def reject(*_: Any) -> dict[str, Any]:
            raise ValueError("audience mismatch")

        credentials = FakeCredentials(id_token="a.b.c")

        email = self._resolve_with(
            fake_google_auth(
                credentials,
                userinfo=FakeResponse(payload={"email": "human@example.com"}),
                verify=reject,
            )
        )

        self.assertEqual(email, "human@example.com")

    def test_falls_back_to_the_userinfo_endpoint(self) -> None:
        credentials = FakeCredentials()
        sessions: list[FakeSession] = []

        email = self._resolve_with(
            fake_google_auth(
                credentials,
                userinfo=FakeResponse(payload={"email": "human@example.com"}),
                sessions=sessions,
            )
        )

        self.assertEqual(email, "human@example.com")
        self.assertTrue(sessions[0].closed)

    def test_refreshes_stale_credentials(self) -> None:
        credentials = FakeCredentials(valid=False, service_account_email="sa@example.com")

        self._resolve_with(fake_google_auth(credentials))

        self.assertTrue(credentials.refreshed)

    def test_returns_none_when_userinfo_rejects_the_call(self) -> None:
        credentials = FakeCredentials()

        email = self._resolve_with(
            fake_google_auth(credentials, userinfo=FakeResponse(status_code=403))
        )

        self.assertIsNone(email)

    def test_returns_none_when_userinfo_omits_the_email(self) -> None:
        credentials = FakeCredentials()

        email = self._resolve_with(fake_google_auth(credentials, userinfo=FakeResponse()))

        self.assertIsNone(email)

    def test_credentials_failure_does_not_propagate(self) -> None:
        """A launcher creating a dashboard must not die because nobody could be named."""

        def no_credentials(scopes: Any) -> Any:
            del scopes
            raise RuntimeError("could not automatically determine credentials")

        google_auth = _GoogleAuth(
            default=no_credentials,
            request_class=lambda: "request",
            authorized_session_class=lambda _: None,
            verify_oauth2_token=lambda *_: {},
        )

        with mock.patch("xdash.auth._load_google_auth", return_value=google_auth):
            self.assertIsNone(resolve_user_email())


class FakeCallDetails:
    """The slice of grpc.ClientCallDetails an interceptor reads and rewrites."""

    def __init__(self, metadata: list[tuple[str, str]] | None = None) -> None:
        self.metadata = metadata

    def _replace(self, metadata: list[tuple[str, str]]) -> "FakeCallDetails":
        return FakeCallDetails(metadata)


class UserEmailInterceptorTest(unittest.TestCase):
    """Covers what reaches the wire, which is the whole point of the module."""

    def setUp(self) -> None:
        self.seen: list[FakeCallDetails] = []

    def _continuation(self, call_details: FakeCallDetails, request: Any) -> str:
        self.seen.append(call_details)
        return f"response:{request}"

    def _intercept(self, interceptor: UserEmailInterceptor, details: FakeCallDetails) -> Any:
        return interceptor.intercept_unary_unary(self._continuation, details, "request")

    def test_sends_an_explicit_email(self) -> None:
        result = self._intercept(UserEmailInterceptor("human@example.com"), FakeCallDetails())

        self.assertEqual(result, "response:request")
        self.assertEqual(self.seen[0].metadata, [(USER_EMAIL_METADATA_KEY, "human@example.com")])

    def test_preserves_metadata_the_caller_already_set(self) -> None:
        details = FakeCallDetails([("x-request-id", "abc")])

        self._intercept(UserEmailInterceptor("human@example.com"), details)

        self.assertEqual(
            self.seen[0].metadata,
            [("x-request-id", "abc"), (USER_EMAIL_METADATA_KEY, "human@example.com")],
        )

    def test_does_not_duplicate_an_identity_the_caller_supplied(self) -> None:
        """Two values for the key would leave the server picking one of two owners."""
        details = FakeCallDetails([("X-Goog-User-Email", "chosen@example.com")])

        self._intercept(UserEmailInterceptor("resolved@example.com"), details)

        self.assertEqual(self.seen[0].metadata, [("X-Goog-User-Email", "chosen@example.com")])

    def test_reads_the_environment_before_the_credentials(self) -> None:
        with mock.patch.dict(os.environ, {USER_EMAIL_ENV_VAR: "override@example.com"}):
            with mock.patch("xdash.auth.resolve_user_email") as resolve:
                self._intercept(UserEmailInterceptor(), FakeCallDetails())

        resolve.assert_not_called()
        self.assertEqual(self.seen[0].metadata, [(USER_EMAIL_METADATA_KEY, "override@example.com")])

    def test_resolves_from_the_credentials_when_nothing_is_configured(self) -> None:
        with mock.patch.dict(os.environ):
            os.environ.pop(USER_EMAIL_ENV_VAR, None)
            with mock.patch("xdash.auth.resolve_user_email", return_value="adc@example.com"):
                self._intercept(UserEmailInterceptor(), FakeCallDetails())

        self.assertEqual(self.seen[0].metadata, [(USER_EMAIL_METADATA_KEY, "adc@example.com")])

    def test_passes_the_call_through_when_nobody_can_be_named(self) -> None:
        """No identity is not an error: the call still goes out, owned by the principal."""
        details = FakeCallDetails()

        with mock.patch.dict(os.environ):
            os.environ.pop(USER_EMAIL_ENV_VAR, None)
            with mock.patch("xdash.auth.resolve_user_email", return_value=None):
                result = self._intercept(UserEmailInterceptor(), details)

        self.assertEqual(result, "response:request")
        self.assertIs(self.seen[0], details)

    def test_resolves_once_across_calls(self) -> None:
        """Resolution can reach the userinfo endpoint; paying that per RPC is not acceptable."""
        interceptor = UserEmailInterceptor()

        with mock.patch.dict(os.environ):
            os.environ.pop(USER_EMAIL_ENV_VAR, None)
            with mock.patch(
                "xdash.auth.resolve_user_email", return_value="adc@example.com"
            ) as resolve:
                self._intercept(interceptor, FakeCallDetails())
                self._intercept(interceptor, FakeCallDetails())

        resolve.assert_called_once()

    def test_a_failed_resolution_is_not_retried(self) -> None:
        interceptor = UserEmailInterceptor()

        with mock.patch.dict(os.environ):
            os.environ.pop(USER_EMAIL_ENV_VAR, None)
            with mock.patch("xdash.auth.resolve_user_email", return_value=None) as resolve:
                self._intercept(interceptor, FakeCallDetails())
                self._intercept(interceptor, FakeCallDetails())

        resolve.assert_called_once()

    def test_resolution_is_deferred_until_the_first_call(self) -> None:
        """Constructing a client must not reach out to the metadata or userinfo endpoints."""
        with mock.patch("xdash.auth.resolve_user_email") as resolve:
            UserEmailInterceptor()

        resolve.assert_not_called()


if __name__ == "__main__":
    unittest.main()
