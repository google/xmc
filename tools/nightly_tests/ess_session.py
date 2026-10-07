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

"""ESS gRPC session lifecycle and error-handling context managers for XMC nightly tests."""

from __future__ import annotations

import contextlib
import sys
from collections.abc import Iterator
from typing import Any

from tools.nightly_tests import endpoint_resolution, xmc_entities_validator


@contextlib.contextmanager
def ess_channel_context(endpoint: str | None = None) -> Iterator[tuple[Any, Any]]:
    """Context manager for authenticated ESS gRPC channel and stub lifecycle."""
    resolved_endpoint = endpoint_resolution.resolve_endpoint(endpoint)
    print(f"Connecting to Experiment State Server at: {resolved_endpoint} ...")
    stub, channel = xmc_entities_validator.create_authenticated_stub(resolved_endpoint)
    try:
        yield stub, channel
    finally:
        if channel is not None and hasattr(channel, "close"):
            channel.close()


@contextlib.contextmanager
def handle_verification_errors() -> Iterator[None]:
    """Context manager handling verification assertion errors and unexpected exceptions."""
    try:
        yield
    except (AssertionError, TimeoutError) as e:
        print(f"\n❌ ASSERTION FAILED: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"\n💥 UNEXPECTED ERROR during verification: {e}", file=sys.stderr)
        sys.exit(2)
