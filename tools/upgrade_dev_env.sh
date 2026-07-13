#!/bin/bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# ==============================================================================
# XMC Environment Upgrade Script (upgrade_dev_env.sh)
# ==============================================================================
# Upgrades an existing deployed XManager on Cloud (XMC) GKE environment to
# match the latest code in the current repository branch.
#
# Leverages the local .env file in the repository root for configuration.
# ==============================================================================

set -Eeo pipefail

# --- Help Menu ---
show_help() {
  echo "Usage: $0 [--env <path_to_dotenv>] [options]"
  echo
  echo "Options:"
  echo "  --env, -e <path>          Path to .env file (Default: ./.env in repository root)"
  echo "  --values, -f <path>       Optional path to custom Helm values.yaml file"
  echo "  --start-from, -s <step>   Start/resume upgrade from this step onwards"
  echo "  --only <step>             Execute ONLY this specific step and exit"
  echo "                             Steps: check, images, app, verify"
  echo "  -h, --help                Show this message"
  exit 0
}

# --- Helper Functions ---

error_exit() {
  echo -e "\n\033[0;31mError: $1\033[0m" >&2
  exit 1
}

log_info() {
  echo -e "\n\033[0;34m[INFO] $1\033[0m"
}

log_success() {
  echo -e "\n\033[0;32m[SUCCESS] $1\033[0m"
}

load_env() {
  local env_file="$1"
  if [[ ! -f "$env_file" ]]; then
    error_exit "Environment configuration file '$env_file' not found. Please copy '.env.example' to '.env' and populate your target environment parameters."
  fi
  log_info "Loading environment variables from '$env_file'..."
  set -o allexport
  # shellcheck disable=SC1090
  source "$env_file"
  set +o allexport

  # Validate required environment variables
  if [[ -z "${PROJECT_ID:-}" ]]; then
    error_exit "PROJECT_ID is missing or empty in '$env_file'."
  fi
  if [[ -z "${GKE_CLUSTER:-}" ]]; then
    error_exit "GKE_CLUSTER is missing or empty in '$env_file'."
  fi

  # Apply defaults for optional or derived variables
  K8S_NAMESPACE="${K8S_NAMESPACE:-xmc}"
  GCP_REGION="${GCP_REGION:-us-central1}"
  XMANAGER_ENV="${XMANAGER_ENV:-dev}"
  XMC_REGISTRY_NAME="${XMC_REGISTRY_NAME:-xmc}"
  XMC_REGISTRY="${XMC_REGISTRY:-us-docker.pkg.dev/${PROJECT_ID}/${XMC_REGISTRY_NAME}}"
  XMC_ACR_TARGET="${XMC_ACR_TARGET:-${XMC_REGISTRY}}"
}

# --- Step Implementations ---

step_check() {
  log_info "Running pre-flight dependency and authentication checks..."

  # 1. Verify required CLI binaries
  local cmd
  for cmd in gcloud kubectl helm bazel; do
    command -v "$cmd" >/dev/null 2>&1 || error_exit "Required CLI tool '$cmd' is not installed or not in PATH."
  done

  # 2. Configure target GCP project
  log_info "Verifying gcloud target project ($PROJECT_ID)..."
  gcloud config set project "$PROJECT_ID" --quiet

  # 3. Configure Docker/Artifact Registry authentication
  local registry_host
  registry_host=$(echo "$XMC_ACR_TARGET" | cut -d'/' -f1)
  if [[ "$registry_host" =~ gcr\.io$ || "$registry_host" =~ \.pkg\.dev$ ]]; then
    log_info "Configuring Docker authentication for Google Artifact Registry ($registry_host)..."
    gcloud auth configure-docker "$registry_host" --quiet
  fi

  # 4. Fetch GKE cluster credentials
  log_info "Fetching kubectl credentials for GKE cluster '$GKE_CLUSTER' in region '$GCP_REGION'..."
  gcloud container clusters get-credentials "$GKE_CLUSTER" \
    --region="$GCP_REGION" \
    --project="$PROJECT_ID"

  # 5. Verify target Kubernetes namespace exists
  if ! kubectl get namespace "$K8S_NAMESPACE" >/dev/null 2>&1; then
    error_exit "Kubernetes namespace '$K8S_NAMESPACE' does not exist on cluster '$GKE_CLUSTER'. Ensure XMC has been deployed first (e.g. via deploy_dev_env.sh or INSTALL.md)."
  fi

  log_success "All pre-flight checks and authentication steps passed successfully."
}

step_images() {
  log_info "Rebuilding and pushing latest XMC microservice OCI images to '$XMC_ACR_TARGET'..."
  export XMC_ENV="$XMANAGER_ENV"

  log_info "Pushing actuator image..."
  bazel run //cmd/actuator:actuator_image_push
  log_info "Pushing experiment_state_server image..."
  bazel run //cmd/experiment_state_server:experiment_state_server_image_push
  log_info "Pushing log_monitor image..."
  bazel run //cmd/log_monitor:log_monitor_image_push
  log_info "Pushing reconciler image..."
  bazel run //cmd/reconciler:reconciler_image_push
  log_info "Pushing ui image..."
  bazel run //cmd/ui:ui_image_push
  log_info "Pushing xid_service image..."
  bazel run //cmd/xid_service:xid_service_image_push

  log_success "All 6 XMC service container images compiled and pushed from current branch state."
}

step_app() {
  log_info "Upgrading XMC Kubernetes application workloads in namespace '$K8S_NAMESPACE'..."

  # 1. Upgrade existing Helm release if present
  if helm status xmc -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
    log_info "Existing Helm release 'xmc' detected. Upgrading release to match local './deploy/helm' templates..."
    if [[ -n "${VALUES_FILE:-}" && -f "$VALUES_FILE" ]]; then
      log_info "Applying custom values from '$VALUES_FILE'..."
      helm upgrade xmc ./deploy/helm -n "$K8S_NAMESPACE" -f "$VALUES_FILE"
    else
      helm upgrade xmc ./deploy/helm -n "$K8S_NAMESPACE" --reuse-values
    fi
    log_success "Helm release 'xmc' upgraded successfully."
  else
    log_info "No existing Helm release 'xmc' found in namespace '$K8S_NAMESPACE' (environment may be gcluster/manifest managed)."
  fi

  # 2. Perform a rolling rollout restart of all XMC service deployments so pods pull newly pushed :latest images
  log_info "Triggering rolling restart across XMC service deployments to run newly pushed container images..."
  local deploy
  for deploy in actuator experiment-state-server log-monitor reconciler ui xid-service; do
    if kubectl get deployment "$deploy" -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
      log_info "Restarting deployment/$deploy..."
      kubectl rollout restart deployment/"$deploy" -n "$K8S_NAMESPACE"
    else
      log_info "Deployment '$deploy' not found in namespace '$K8S_NAMESPACE'. Skipping."
    fi
  done

  log_success "Application upgrade and rolling deployment restarts initiated."
}

step_verify() {
  log_info "Verifying rollout completion and cluster health in namespace '$K8S_NAMESPACE'..."

  local deploy
  for deploy in actuator experiment-state-server log-monitor reconciler ui xid-service; do
    if kubectl get deployment "$deploy" -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
      log_info "Waiting for rollout status on deployment/$deploy (timeout 300s)..."
      kubectl rollout status deployment/"$deploy" -n "$K8S_NAMESPACE" --timeout=300s
    fi
  done

  # Check Gateway status if available
  if kubectl get gateway xmc-gateway -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
    log_info "Verifying GKE Gateway programmed condition..."
    kubectl wait --for=condition=Programmed gateway/xmc-gateway -n "$K8S_NAMESPACE" --timeout=120s || \
      log_info "Warning: Gateway xmc-gateway not yet reporting Programmed=True condition."
  fi

  log_success "XMC environment upgrade to current repository branch state verified successfully."
}

# --- Execution Entrypoint ---

ENV_FILE="./.env"
VALUES_FILE=""
START_STEP=""
ONLY_STEP=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env|-e)
      ENV_FILE="$2"
      shift 2
      ;;
    --values|-f)
      VALUES_FILE="$2"
      shift 2
      ;;
    --start-from|-s)
      START_STEP="$2"
      shift 2
      ;;
    --only)
      ONLY_STEP="$2"
      shift 2
      ;;
    -h|--help)
      show_help
      ;;
    *)
      error_exit "Unknown argument: $1. Use --help to view available usage."
      ;;
  esac
done

load_env "$ENV_FILE"

STEPS=("check" "images" "app" "verify")

if [[ -n "$ONLY_STEP" ]]; then
  case "$ONLY_STEP" in
    check) step_check ;;
    images) step_images ;;
    app) step_app ;;
    verify) step_verify ;;
    *) error_exit "Invalid step '$ONLY_STEP' for --only. Valid steps: check, images, app, verify" ;;
  esac
  exit 0
fi

SHOULD_RUN=true
if [[ -n "$START_STEP" ]]; then
  SHOULD_RUN=false
fi

for step in "${STEPS[@]}"; do
  if [[ "$step" == "$START_STEP" ]]; then
    SHOULD_RUN=true
  fi
  if [[ "$SHOULD_RUN" == "true" ]]; then
    case "$step" in
      check) step_check ;;
      images) step_images ;;
      app) step_app ;;
      verify) step_verify ;;
    esac
  fi
done

log_success "Upgrade completed end-to-end successfully!"
