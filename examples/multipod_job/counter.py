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

"""Simple counter script that prints sequential integers for N minutes."""

import os
import time


def main() -> None:
    """Runs counter printing loop for specified duration."""
    duration_minutes = float(os.environ.get("DURATION_MINUTES", "2"))
    prefix = os.environ.get("POD_PREFIX", os.environ.get("HOSTNAME", "counter"))

    print(
        f"[{prefix}] Starting counter for {duration_minutes} minutes...",
        flush=True,
    )

    start_time = time.time()
    end_time = start_time + (duration_minutes * 60.0)
    counter = 1

    while time.time() < end_time:
        print(f"[{prefix}] {counter}", flush=True)
        counter += 1
        time.sleep(3.0)

    print(
        f"[{prefix}] Finished counting after {duration_minutes} minutes.",
        flush=True,
    )


if __name__ == "__main__":
    main()
