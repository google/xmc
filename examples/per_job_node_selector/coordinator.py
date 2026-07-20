#!/usr/bin/env python3
"""Head coordinator script running on the 'cpups' CPU node pool."""

import os
import time


def main() -> None:
    role = os.environ.get("POD_ROLE", "head")
    num_workers = int(os.environ.get("NUM_WORKERS", "2"))
    hostname = os.environ.get("HOSTNAME", "head-pod")

    print(
        f"[{role}] Coordinator {hostname} initialized on CPU node pool.",
        flush=True,
    )
    print(f"[{role}] Expecting {num_workers} worker replicas to connect.", flush=True)

    for iteration in range(1, 6):
        print(f"[{role}] Orchestrating training epoch {iteration}...", flush=True)
        time.sleep(2.0)

    print(f"[{role}] All epochs completed successfully.", flush=True)


if __name__ == "__main__":
    main()
