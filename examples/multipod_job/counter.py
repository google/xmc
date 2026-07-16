#!/usr/bin/env python3
"""Simple counter script that prints sequential integers for N minutes."""

import os
import sys
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
