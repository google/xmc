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

"""Worker script running on the 'gpu-pool' accelerator node pool."""

import os
import time


def main() -> None:
    role = os.environ.get("POD_ROLE", "worker")
    coordinator_addr = os.environ.get("COORDINATOR_ADDR", "head-head-0-0")
    hostname = os.environ.get("HOSTNAME", "worker-pod")

    print(
        f"[{role}] Worker {hostname} initialized on GPU accelerator pool.",
        flush=True,
    )
    print(f"[{role}] Connecting to coordinator at {coordinator_addr}...", flush=True)

    for iteration in range(1, 6):
        print(f"[{role}] Executing step {iteration} on accelerator...", flush=True)
        time.sleep(2.0)

    print(f"[{role}] Worker execution finished.", flush=True)


if __name__ == "__main__":
    main()
