#!/bin/bash
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

# Verification Script for XManager on Cloud v2 Topology

echo "========================================="
echo "XManager on Cloud (XMC) v2 Verification"
echo "========================================="

# --- Utility Functions ---
get_var() {
  local var_name="${1}"
  local file="${2}"
  grep -E "^\s*${var_name}:" "${file}" | head -n 1 | awk '{print $2}' | tr -d '"' | tr -d "'"
}

verify_log_monitor() {
  echo -e "\n--- 7. Cloud Run Log Monitor Status ---"
  if [ -z "${ENVIRONMENT}" ] || [ -z "${REGION}" ] || [ -z "${PROJECT_ID}" ]; then
    echo "⚠️ Skipping Log Monitor check: ENVIRONMENT, REGION, or PROJECT_ID is not set."
    return
  fi

  local SVC_NAME="xmc-log-monitor-${ENVIRONMENT}"
  echo "Checking Cloud Run Service: ${SVC_NAME} in ${REGION}..."

  # Retrieve the Ready condition status (also checks for service existence)
  local STATUS=$(gcloud run services list --filter="metadata.name=${SVC_NAME} AND status.conditions.type=Ready" --flatten="status.conditions" --format="value(status.conditions.status)" --platform managed --region "${REGION}" --project "${PROJECT_ID}")

  if [ -z "${STATUS}" ]; then
    echo "❌ Log Monitor Cloud Run service ${SVC_NAME} not found or status is unavailable."
    return
  fi

  echo "Service Status (Ready): ${STATUS}"

  if [ "${STATUS}" != "True" ]; then
    echo "❌ Log Monitor Cloud Run service is NOT Ready."
    return
  fi

  echo "✅ Log Monitor Cloud Run service is running and Ready."
}

# --- Auto-detect config ---
CONFIG_FILE="$(dirname "$0")/xmanager-cloud-deployment.yaml"
if [ -f "${CONFIG_FILE}" ]; then
  ENVIRONMENT="${ENVIRONMENT:-$(get_var "environment" "${CONFIG_FILE}")}"
  REGION="${REGION:-$(get_var "region" "${CONFIG_FILE}")}"
  PROJECT_ID="${PROJECT_ID:-$(get_var "project_id" "${CONFIG_FILE}")}"
fi

echo -e "\n--- 1. Pod Status ---"
kubectl get pods -n xmc

echo -e "\n--- 2. Service Status ---"
kubectl get svc -n xmc

echo -e "\n--- 3. GKE Gateway Status ---"
kubectl get gateway -n xmc

echo -e "\n--- 4. HTTPRoutes ---"
kubectl get httproute -n xmc

echo -e "\n--- 5. cert-manager Certificates ---"
kubectl get certificate -n xmc

echo -e "\n--- 6. Test Connectivity ---"
GATEWAY_IP=$(kubectl get gateway xmc-gateway -n xmc -o jsonpath='{.status.addresses[0].value}')
echo "Gateway IP Address: $GATEWAY_IP"
echo "Make sure your domains resolve to $GATEWAY_IP."

echo -e "\nTo test local endpoint routing internally (Port-Forwarding Actuator):"
ACTUATOR_POD=$(kubectl get pods -n xmc -l app=actuator -o jsonpath='{.items[0].metadata.name}')
echo "Run: kubectl port-forward -n xmc $ACTUATOR_POD 8080:8080"
echo "In another terminal run: curl http://localhost:8080/actuate/health"

verify_log_monitor

echo "========================================="
