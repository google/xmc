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

"""Algorithms the dev/test Vizier fixture accepts.

The fixture image does not ship the packages the Gaussian-process designers need (the DEFAULT
algorithm included), so studies may only use designers that run on its pinned dependencies.
Kept free of google-vizier imports so it can be unit tested without them.
"""

SUPPORTED_ALGORITHMS = ("RANDOM_SEARCH", "QUASI_RANDOM_SEARCH", "GRID_SEARCH")


def unsupported_algorithm_error(algorithm: str) -> str | None:
    """Returns why a study algorithm is rejected, or None when it is supported.

    Args:
        algorithm: StudySpec.algorithm, e.g. "RANDOM_SEARCH". Unset algorithms ("",
            "ALGORITHM_UNSPECIFIED", "DEFAULT") are unsupported.

    Returns:
        An error message naming the algorithm and the supported ones, or None.
    """
    if algorithm in SUPPORTED_ALGORITHMS:
        return None
    return (
        f"algorithm {algorithm or 'ALGORITHM_UNSPECIFIED'!r} is not supported by this Vizier "
        f"fixture; use one of {', '.join(SUPPORTED_ALGORITHMS)}"
    )
