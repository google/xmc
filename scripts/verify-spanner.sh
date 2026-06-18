#!/bin/bash
# scripts/verify-spanner.sh
set -e

HOST=$(echo "${SPANNER_EMULATOR_HOST}" | cut -d: -f1)
PORT=$(echo "${SPANNER_EMULATOR_HOST}" | cut -d: -f2)

for i in $(seq 1 "${SPANNER_CHECK_TIMEOUT}"); do
  if nc -z "$HOST" "$PORT" >/dev/null 2>&1 || timeout 1s bash -c "cat < /dev/null > /dev/tcp/$HOST/$PORT" 2>/dev/null; then
    echo "Successfully connected to Spanner Emulator at ${SPANNER_EMULATOR_HOST}."
    exit 0
  fi
  echo "Waiting for Spanner Emulator to start (attempt $i/${SPANNER_CHECK_TIMEOUT})..."
  sleep 1
done

echo "CRITICAL: Spanner Emulator at ${SPANNER_EMULATOR_HOST} did not start within ${SPANNER_CHECK_TIMEOUT} seconds."
exit 1
