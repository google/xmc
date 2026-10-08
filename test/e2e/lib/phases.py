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

"""Lifecycle phases of an E2E harness attempt and phase-tagged failures."""

from __future__ import annotations

import contextlib
import enum
from collections.abc import Generator


class Phase(enum.Enum):
    """Lifecycle phases of one harness attempt, in execution order."""

    IMAGE_BUILD = "image-build"
    KIND_CREATE = "kind-create"
    IMAGE_LOAD = "image-load"
    SPANNER_SETUP = "spanner-setup"
    HELM_DEPLOY = "helm-deploy"
    TESTS = "tests"

    @property
    def is_infra(self) -> bool:
        """Whether a failure of this phase is an infra failure rather than a product failure.

        Only the phases that provision CI infrastructure are infra phases: the KinD cluster,
        the image load into it, and the Spanner databases. The image build, the Helm
        deployment, and the tests exercise the code under test.
        """
        return self in _INFRA_PHASES


_INFRA_PHASES = frozenset({Phase.KIND_CREATE, Phase.IMAGE_LOAD, Phase.SPANNER_SETUP})


class PhaseError(Exception):
    """Raised when a lifecycle phase fails; the original exception is its ``__cause__``.

    Attributes:
        phase: The lifecycle phase that failed.
    """

    def __init__(self, phase: Phase) -> None:
        super().__init__(f"E2E lifecycle phase '{phase.value}' failed")
        self.phase = phase


@contextlib.contextmanager
def phase_scope(phase: Phase) -> Generator[None, None, None]:
    """Re-raises any ``Exception`` escaping the block as a ``PhaseError`` for ``phase``.

    A ``PhaseError`` from a nested scope propagates unchanged so the innermost phase wins.
    ``BaseException``s such as the ``SystemExit`` raised by the signal handler are never
    wrapped, so stopped runs are not classified by phase.

    Args:
        phase: The lifecycle phase executed inside the block.

    Raises:
        PhaseError: If the block raises an ``Exception``.
    """
    try:
        yield
    except PhaseError:
        raise
    except Exception as exc:
        raise PhaseError(phase) from exc
