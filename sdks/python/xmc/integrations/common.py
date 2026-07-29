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

"""Common utilities for XMC framework integrations."""

import re
from typing import Any


def parse_version(version_str: str) -> tuple[int, ...]:
    """Parses a version string into an integer tuple for comparison.

    Pads single or two-part version strings (e.g. "2.0" -> (2, 0, 0)) to ensure
    consistent tuple length comparison in Python.
    """
    parts = []
    for segment in version_str.split(".")[:3]:
        match = re.match(r"\d+", segment)
        parts.append(int(match.group(0)) if match else 0)
    while len(parts) < 3:
        parts.append(0)
    return tuple(parts)


def extract_scalar(val: Any) -> float | int | None:
    """Extracts a primitive float or int scalar from various framework tensor types.

    Supports PyTorch, TensorFlow, JAX, NumPy 0d arrays, and standard Python numbers.
    """
    if isinstance(val, (int, float)):
        return val

    if hasattr(val, "item") and callable(val.item):
        try:
            res = val.item()
            if isinstance(res, (int, float)):
                return res
        except Exception:
            pass

    if hasattr(val, "numpy") and callable(val.numpy):
        try:
            n_val = val.numpy()
            if isinstance(n_val, (int, float)):
                return n_val
            if hasattr(n_val, "item") and callable(n_val.item):
                res = n_val.item()
                if isinstance(res, (int, float)):
                    return res
            return float(n_val)
        except Exception:
            pass

    try:
        return float(val)
    except Exception:
        return None
