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

"""A dummy worker script that simulates work."""

import time


def main():
    print("Worker started. Simulating training and generating charts...")
    for i in range(1, 4):
        print(f"Step {i}: accuracy = {0.5 + (0.1 * i):.2f}, loss = {1.0 - (0.2 * i):.2f}")
        time.sleep(10)
    print("Worker finished successfully.")


if __name__ == "__main__":
    main()
