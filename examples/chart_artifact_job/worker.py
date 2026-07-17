#!/usr/bin/env python3
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
