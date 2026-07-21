#!/usr/bin/env python3
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

"""Initialization step running as an XMC Init Container."""

import os


def main() -> None:
    """Executes initialization sequence and writes marker flag."""
    pod_name = os.environ.get("HOSTNAME", "unknown-pod")
    prefix = os.environ.get("POD_PREFIX", "init-setup")
    print(f"[{prefix} | {pod_name}] Starting initialization step...", flush=True)

    flag_path = "/dev/disk-scratch/init_ready.flag"
    with open(flag_path, "w", encoding="utf-8") as f:
        f.write(f"Initialized by {prefix} on {pod_name}\n")

    print(
        f"[{prefix} | {pod_name}] Successfully created marker {flag_path}. Init complete!",
        flush=True,
    )


if __name__ == "__main__":
    main()
