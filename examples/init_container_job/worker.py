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

"""Primary application worker running after the Init Container completes."""

import os
import time


def main() -> None:
    """Executes worker heartbeat loop after checking init marker."""
    pod_name = os.environ.get("HOSTNAME", "unknown-pod")
    prefix = os.environ.get("POD_PREFIX", "primary-worker")
    flag_path = "/dev/disk-scratch/init_ready.flag"

    print(
        f"[{prefix} | {pod_name}] Starting primary worker execution...",
        flush=True,
    )

    if os.path.exists(flag_path):
        with open(flag_path, encoding="utf-8") as f:
            content = f.read().strip()
        print(
            f"[{prefix} | {pod_name}] Verified init marker: '{content}'",
            flush=True,
        )
    else:
        print(
            f"[{prefix} | {pod_name}] Note: Marker {flag_path} not found on this filesystem.",
            flush=True,
        )

    duration_mins = int(os.environ.get("DURATION_MINUTES", "1"))
    end_time = time.time() + (duration_mins * 60)
    counter = 1
    while time.time() < end_time:
        print(f"[{prefix} | {pod_name}] Heartbeat tick #{counter}", flush=True)
        counter += 1
        time.sleep(10)

    print(f"[{prefix} | {pod_name}] Worker completed successfully.", flush=True)


if __name__ == "__main__":
    main()
