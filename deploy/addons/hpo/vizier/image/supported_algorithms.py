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

"""Algorithms the Vizier server image supports.

The image does not ship the packages that the Gaussian-process designers need (the DEFAULT
algorithm included), so only designers that run on the pinned dependencies are allowed. Kept free
of google-vizier imports so it can be unit tested without the server's runtime dependencies.
"""

SUPPORTED_ALGORITHMS = ("RANDOM_SEARCH", "QUASI_RANDOM_SEARCH", "GRID_SEARCH")


class UnsupportedAlgorithmError(ValueError):
    """Raised for a study algorithm the image does not support."""


def check_algorithm(algorithm: str) -> None:
    """Raises UnsupportedAlgorithmError unless algorithm is in SUPPORTED_ALGORITHMS.

    Args:
        algorithm: Algorithm name of a study spec, e.g. "RANDOM_SEARCH". Unset algorithms
            ("", "ALGORITHM_UNSPECIFIED", "DEFAULT") are unsupported.

    Raises:
        UnsupportedAlgorithmError: The algorithm is not supported.
    """
    if algorithm in SUPPORTED_ALGORITHMS:
        return
    raise UnsupportedAlgorithmError(
        f"algorithm {algorithm or 'ALGORITHM_UNSPECIFIED'!r} is not supported by this Vizier "
        f"server; use one of {', '.join(SUPPORTED_ALGORITHMS)}"
    )
