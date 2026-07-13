#!/usr/bin/env python3
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
  print(
      f"[{role}] Connecting to coordinator at {coordinator_addr}...", flush=True
  )

  for iteration in range(1, 6):
    print(f"[{role}] Executing step {iteration} on accelerator...", flush=True)
    time.sleep(2.0)

  print(f"[{role}] Worker execution finished.", flush=True)


if __name__ == "__main__":
  main()
