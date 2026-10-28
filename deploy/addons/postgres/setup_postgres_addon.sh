#!/usr/bin/env bash
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

# Deploys a development PostgreSQL into the `xmc` namespace, creates the `xmc-postgres-credentials`
# Secret, and applies the ESS and XID schema migrations with the postgres-migrator image.
#
# Usage:
#   MIGRATOR_IMAGE=<registry>/postgres-migrator-<env>:<tag> deploy/addons/postgres/setup_postgres_addon.sh
#
# Environment:
#   MIGRATOR_IMAGE   Required. Image built from //internal/storage/postgres/migrations:postgres_migrator_image.
#   XMC_NAMESPACE    Namespace to deploy into (default: xmc).
#   POSTGRES_USER    Database superuser name stored in the Secret (default: xmc).
#   STORAGE_CLASS    Optional StorageClassName for the data PVC (default: cluster default).
#   SKIP_MIGRATIONS  Set to 1 to only deploy the database.
#
# Afterwards install the chart with:
#   --set database.backend=postgres \
#   --set database.postgres.host=postgres.<namespace>.svc.cluster.local \
#   --set database.postgres.user=<POSTGRES_USER> \
#   --set database.postgres.essDatabase=ess --set database.postgres.xidDatabase=xid \
#   --set database.postgres.sslMode=disable

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly XMC_NAMESPACE="${XMC_NAMESPACE:-xmc}"
readonly POSTGRES_USER="${POSTGRES_USER:-xmc}"
readonly SECRET_NAME="xmc-postgres-credentials"
readonly PG_HOST="postgres.${XMC_NAMESPACE}.svc.cluster.local"

echo "--> 1. Ensuring namespace '${XMC_NAMESPACE}' exists..."
kubectl create namespace "${XMC_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

echo "--> 2. Ensuring Secret '${SECRET_NAME}' exists (random password on first run)..."
if ! kubectl get secret "${SECRET_NAME}" -n "${XMC_NAMESPACE}" &>/dev/null; then
  password="$(head -c 32 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32)"
  kubectl create secret generic "${SECRET_NAME}" -n "${XMC_NAMESPACE}" \
    --from-literal=username="${POSTGRES_USER}" \
    --from-literal=password="${password}"
fi

echo "--> 3. Deploying PostgreSQL..."
manifest="$(cat "${SCRIPT_DIR}/manifests/postgres.yaml")"
if [[ -n "${STORAGE_CLASS:-}" ]]; then
  manifest="${manifest//  accessModes: [\"ReadWriteOnce\"]/  storageClassName: ${STORAGE_CLASS}
  accessModes: [\"ReadWriteOnce\"]}"
fi
echo "${manifest}" | sed "s/namespace: xmc$/namespace: ${XMC_NAMESPACE}/" | kubectl apply -f -

echo "--> 4. Waiting for PostgreSQL rollout..."
kubectl rollout status deployment/postgres -n "${XMC_NAMESPACE}" --timeout=300s

if [[ "${SKIP_MIGRATIONS:-0}" == "1" ]]; then
  echo "SKIP_MIGRATIONS=1: database deployed, migrations skipped."
  exit 0
fi

if [[ -z "${MIGRATOR_IMAGE:-}" ]]; then
  echo "ERROR: MIGRATOR_IMAGE is not set (build //internal/storage/postgres/migrations:postgres_migrator_image)." >&2
  exit 1
fi

run_migration() {
  local dir="$1" db="$2"
  local job="postgres-migrate-${dir}-$(date +%s)"
  echo "--> Applying '${dir}' migrations to database '${db}' (job ${job})..."
  kubectl apply -n "${XMC_NAMESPACE}" -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${job}
  labels:
    app: postgres-migrator
spec:
  backoffLimit: 3
  ttlSecondsAfterFinished: 3600
  template:
    metadata:
      labels:
        app: postgres-migrator
    spec:
      restartPolicy: Never
      containers:
      - name: migrator
        image: ${MIGRATOR_IMAGE}
        args: ["--command", "up", "--migrations_dir", "${dir}"]
        env:
        - name: PGUSER
          valueFrom:
            secretKeyRef:
              name: ${SECRET_NAME}
              key: username
        - name: PGPASSWORD
          valueFrom:
            secretKeyRef:
              name: ${SECRET_NAME}
              key: password
        - name: POSTGRES_DSN
          value: "postgres://\$(PGUSER):\$(PGPASSWORD)@${PG_HOST}:5432/${db}?sslmode=disable"
EOF
  kubectl wait --for=condition=complete "job/${job}" -n "${XMC_NAMESPACE}" --timeout=300s || {
    echo "Migration job ${job} did not complete; logs:" >&2
    kubectl logs "job/${job}" -n "${XMC_NAMESPACE}" >&2 || true
    exit 1
  }
  kubectl logs "job/${job}" -n "${XMC_NAMESPACE}" | tail -5
}

run_migration ess ess
run_migration xid xid

cat <<EOF

✅ PostgreSQL is ready at ${PG_HOST}:5432 (databases: ess, xid; Secret: ${SECRET_NAME}).

Helm values for the chart:
  database:
    backend: postgres
    postgres:
      host: ${PG_HOST}
      port: 5432
      user: ${POSTGRES_USER}
      essDatabase: ess
      xidDatabase: xid
      sslMode: disable
      passwordSecret:
        name: ${SECRET_NAME}
        key: password
EOF
