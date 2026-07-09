#!/usr/bin/env python3
"""Primary application worker running after the Init Container completes."""

import os
import sys
import time


def main() -> None:
  pod_name = os.environ.get("HOSTNAME", "unknown-pod")
  prefix = os.environ.get("POD_PREFIX", "primary-worker")
  flag_path = "/dev/disk-scratch/init_ready.flag"

  print(
      f"[{prefix} | {pod_name}] Starting primary worker execution...",
      flush=True,
  )

  if os.path.exists(flag_path):
    with open(flag_path, "r", encoding="utf-8") as f:
      content = f.read().strip()
    print(
        f"[{prefix} | {pod_name}] Verified init marker: '{content}'",
        flush=True,
    )
  else:
    print(
        f"[{prefix} | {pod_name}] Note: Marker {flag_path} not found on this"
        " filesystem.",
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
