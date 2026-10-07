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

"""Formatted console banner and summary reporting helpers for XMC nightly tests."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any


def print_banner(title: str, details: Mapping[str, Any] | Sequence[tuple[str, Any]]) -> None:
    """Prints a formatted verification header banner."""
    print("=================================================================")
    print(f"       {title.strip()}       ".center(65))
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"{label:<24}: {val_str}")
    print("-----------------------------------------------------------------")


def print_success_summary(
    title: str,
    details: Mapping[str, Any] | Sequence[tuple[str, Any]],
) -> None:
    """Prints a formatted verification success summary."""
    print("\n=================================================================")
    print(f"🎉 {title.strip()}")
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"  • {label:<24}: {val_str}")
    print("=================================================================\n")
