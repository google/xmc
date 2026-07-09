#!/usr/bin/env python3
"""Initialization step running as an XMC Init Container."""

import os
import time


def main() -> None:
  pod_name = os.environ.get("HOSTNAME", "unknown-pod")
  prefix = os.environ.get("POD_PREFIX", "init-setup")
  print(f"[{prefix} | {pod_name}] Starting initialization step...", flush=True)

  flag_path = "/tmp/init_ready.flag"
  with open(flag_path, "w", encoding="utf-8") as f:
    f.write(f"Initialized by {prefix} on {pod_name}\n")

  print(
      f"[{prefix} | {pod_name}] Successfully created marker {flag_path}."
      " Init complete!",
      flush=True,
  )


if __name__ == "__main__":
  main()
