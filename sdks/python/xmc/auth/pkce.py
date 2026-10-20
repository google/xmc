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

"""PKCE (RFC 7636) S256 code verifiers/challenges and OAuth `state` values."""

import base64
import hashlib
import secrets


def s256_challenge(verifier: str) -> str:
    """Returns BASE64URL(SHA256(verifier)) without padding (RFC 7636 section 4.2)."""
    digest = hashlib.sha256(verifier.encode("ascii")).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def new_verifier() -> str:
    """Returns a random 86-character code verifier (RFC 7636 allows 43-128)."""
    return secrets.token_urlsafe(64)


def new_state() -> str:
    """Returns an unguessable OAuth `state` value carrying 256 random bits."""
    return secrets.token_urlsafe(32)
