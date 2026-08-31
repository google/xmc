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

"""Mints IAP-audience OIDC identity tokens for XMC HTTP endpoints."""

from __future__ import annotations

import google.auth
import google.auth.transport.requests

_GENERATE_ID_TOKEN_URL = (
    "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa}:generateIdToken"
)


def mint_identity_token(service_account: str, audience: str) -> str:
    """Mints an OIDC identity token by impersonating a client service account.

    This is the same exchange the XMC CLI performs before calling the Experiment State
    Server. The gateway in front of the Dashboard Service accepts the resulting token as
    an `Authorization: Bearer` credential.

    Args:
        service_account: Client service account to impersonate (`XMC_CLIENT_SA`).
        audience: OAuth client ID the token is minted for (`IAP_CLIENT_ID`).

    Returns:
        The signed OIDC identity token.

    Raises:
        RuntimeError: If application default credentials are unavailable, the caller
            cannot impersonate the service account, or the exchange fails.
    """
    try:
        credentials, _ = google.auth.default()
        session = google.auth.transport.requests.AuthorizedSession(credentials)
        response = session.post(
            _GENERATE_ID_TOKEN_URL.format(sa=service_account),
            json={"audience": audience, "includeEmail": True},
        )
        response.raise_for_status()
        return str(response.json()["token"])
    except Exception as err:
        raise RuntimeError(
            f"failed to mint an identity token for audience {audience} as {service_account}"
        ) from err
