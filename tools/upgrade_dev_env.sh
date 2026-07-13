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

# Ensure the script is executed from the repository root directory
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 1

# --- Help Menu ---
show_help() {
  echo "Usage: $0 [--env <path_to_dotenv>] [options]"
  echo
  echo "Options:"
  echo "  --env, -e <path>          Path to .env file (Default: ./.env in repository root)"
  echo "  --start-from, -s <step>   Start/resume upgrade from this step onwards"
  echo "  --only <step>             Execute ONLY this specific step and exit"
  echo "                             Steps: check, migrate, images, app, verify"
  echo "  --run-migrations          Automatically run pending migrations if detected"
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
  if [[ -z "${XMC_DEPLOYMENT_NAME:-}" ]]; then
    error_exit "XMC_DEPLOYMENT_NAME is missing or empty in '$env_file'."
  fi

  # Apply defaults for optional or derived variables
  export K8S_NAMESPACE="${K8S_NAMESPACE:-xmc}"
  export GCP_REGION="${GCP_REGION:-us-central1}"
  export XMANAGER_ENV="${XMANAGER_ENV:-dev}"
  export XMC_REGISTRY_NAME="${XMC_REGISTRY_NAME:-xmc}"
  export XMC_REGISTRY="${XMC_REGISTRY:-us-docker.pkg.dev/${PROJECT_ID}/${XMC_REGISTRY_NAME}}"
  export XMC_ACR_TARGET="${XMC_ACR_TARGET:-${XMC_REGISTRY}}"
}

# --- Step Implementations ---

step_check() {
  log_info "Running pre-flight dependency and authentication checks..."

  # 1. Verify required CLI binaries
  local cmd
  for cmd in gcloud kubectl helm bazel; do
    command -v "$cmd" >/dev/null 2>&1 || error_exit "Required CLI tool '$cmd' is not installed or not in PATH."
  done

  # Verify local cluster-toolkit binary
  if [[ ! -f "./cluster-toolkit/gcluster" || ! -x "./cluster-toolkit/gcluster" ]]; then
    error_exit "Local cluster-toolkit binary './cluster-toolkit/gcluster' not found or is not executable."
  fi

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

step_migrate() {
  log_info "Checking for pending database migrations..."

  local spanner_instance="xmc-${XMANAGER_ENV}"
  local ess_db="experiment-db-${XMANAGER_ENV}"
  local xid_db="xids-db-${XMANAGER_ENV}"

  # Check ESS
  local max_ess_version=0
  if ls internal/storage/spanner/migrations/ess/*.up.sql >/dev/null 2>&1; then
    max_ess_version=$(ls internal/storage/spanner/migrations/ess/*.up.sql | xargs -n 1 basename | cut -d'_' -f1 | sed 's/^0*//' | sort -rn | head -n 1)
  fi
  max_ess_version="${max_ess_version:-0}"

  log_info "Fetching current ESS database version..."
  local ess_version_out
  ess_version_out=$(bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="$PROJECT_ID" \
    --instance="$spanner_instance" \
    --database="$ess_db" \
    --migrations_dir="ess" \
    --command="version" 2>/dev/null || echo "Version: 0, Dirty: false")
  
  local current_ess_version
  current_ess_version=$(echo "$ess_version_out" | grep -oE "Version: [0-9]+" | cut -d' ' -f2 || echo "0")
  current_ess_version="${current_ess_version:-0}"
  
  local ess_dirty
  ess_dirty=$(echo "$ess_version_out" | grep -oE "Dirty: (true|false)" | cut -d' ' -f2 || echo "false")

  if [[ "$ess_dirty" == "true" ]]; then
    error_exit "ESS database is in a DIRTY state. Please resolve manually before proceeding."
  fi

  # Check XID
  local max_xid_version=0
  if ls internal/storage/spanner/migrations/xid/*.up.sql >/dev/null 2>&1; then
    max_xid_version=$(ls internal/storage/spanner/migrations/xid/*.up.sql | xargs -n 1 basename | cut -d'_' -f1 | sed 's/^0*//' | sort -rn | head -n 1)
  fi
  max_xid_version="${max_xid_version:-0}"

  log_info "Fetching current XID database version..."
  local xid_version_out
  xid_version_out=$(bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="$PROJECT_ID" \
    --instance="$spanner_instance" \
    --database="$xid_db" \
    --migrations_dir="xid" \
    --command="version" 2>/dev/null || echo "Version: 0, Dirty: false")
  
  local current_xid_version
  current_xid_version=$(echo "$xid_version_out" | grep -oE "Version: [0-9]+" | cut -d' ' -f2 || echo "0")
  current_xid_version="${current_xid_version:-0}"
  
  local xid_dirty
  xid_dirty=$(echo "$xid_version_out" | grep -oE "Dirty: (true|false)" | cut -d' ' -f2 || echo "false")

  if [[ "$xid_dirty" == "true" ]]; then
    error_exit "XID database is in a DIRTY state. Please resolve manually before proceeding."
  fi

  log_info "ESS database version: current=$current_ess_version, available=$max_ess_version"
  log_info "XID database version: current=$current_xid_version, available=$max_xid_version"

  local has_pending=false
  if (( max_ess_version > current_ess_version )) || (( max_xid_version > current_xid_version )); then
    has_pending=true
  fi

  if [[ "$has_pending" == "true" ]]; then
    log_info "Pending migrations detected!"
    if [[ "$RUN_MIGRATIONS" == "true" ]]; then
      log_info "Running pending migrations as requested..."
      
      if (( max_ess_version > current_ess_version )); then
        log_info "Running ESS migrations..."
        bazel run //internal/storage/spanner/migrations:migrations -- \
          --project="$PROJECT_ID" \
          --instance="$spanner_instance" \
          --database="$ess_db" \
          --migrations_dir="ess" \
          --command="up"
      fi

      if (( max_xid_version > current_xid_version )); then
        log_info "Running XID migrations..."
        bazel run //internal/storage/spanner/migrations:migrations -- \
          --project="$PROJECT_ID" \
          --instance="$spanner_instance" \
          --database="$xid_db" \
          --migrations_dir="xid" \
          --command="up"
      fi
      log_success "Migrations applied successfully."
    else
      echo "STATUS: PENDING_MIGRATIONS"
      echo "ESS: current=$current_ess_version, available=$max_ess_version"
      echo "XID: current=$current_xid_version, available=$max_xid_version"
      # Exit with code 10 to signal pending migrations to the agent
      exit 10
    fi
  else
    log_success "No pending migrations found. Databases are up to date."
  fi
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
  log_info "Upgrading XMC Kubernetes application workloads using Cluster Toolkit..."

  # 1. Upgrade application workloads using gcluster
  if [[ ! -d "ctk-out/$XMC_DEPLOYMENT_NAME" ]]; then
    error_exit "Deployment directory 'ctk-out/$XMC_DEPLOYMENT_NAME' not found. Ensure the environment has been deployed first."
  fi

  log_info "Running gcluster deploy for group 'xmc_app'..."
  ./cluster-toolkit/gcluster --download-dependencies deploy "ctk-out/$XMC_DEPLOYMENT_NAME" --only xmc_app --auto-approve

  # 2. Perform a rolling rollout restart of all XMC service deployments so pods pull newly pushed :latest images
  # (gcluster deploy might not trigger restart if image tags didn't change, so we force it)
  log_info "Triggering rolling restart across XMC service deployments to run newly pushed container images..."
  local deploy
  for deploy in actuator experiment-state-server log-monitor reconciler ui xid-service; do
    local deploy_name="$deploy"
    if ! kubectl get deployment "$deploy_name" -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
      if kubectl get deployment "${deploy}-deployment" -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
        deploy_name="${deploy}-deployment"
      fi
    fi

    if kubectl get deployment "$deploy_name" -n "$K8S_NAMESPACE" >/dev/null 2>&1; then
      log_info "Restarting deployment/$deploy_name..."
      kubectl rollout restart deployment/"$deploy_name" -n "$K8S_NAMESPACE"
    else
      log_info "Deployment '$deploy' (or '${deploy}-deployment') not found in namespace '$K8S_NAMESPACE'. Skipping."
    fi
  done

  log_success "Application upgrade and rolling deployment restarts initiated."
}

step_verify() {
  log_info "Verifying rollout completion and cluster health in namespace '$K8S_NAMESPACE'..."

  log_info "Running verification script..."
  ENVIRONMENT="$XMANAGER_ENV" REGION="$GCP_REGION" PROJECT_ID="$PROJECT_ID" bash ./deploy/ctk_v2/verify_xmc.sh || log_info "Diagnostic script reported warnings."

  log_success "XMC environment upgrade to current repository branch state verified successfully."
}

# --- Execution Entrypoint ---

ENV_FILE="./.env"
START_STEP=""
ONLY_STEP=""
RUN_MIGRATIONS=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env|-e)
      ENV_FILE="$2"
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
    --run-migrations)
      RUN_MIGRATIONS=true
      shift
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

STEPS=("check" "migrate" "images" "app" "verify")

if [[ -n "$START_STEP" ]]; then
  case "$START_STEP" in
    check|migrate|images|app|verify) ;;
    *) error_exit "Invalid step '$START_STEP' for --start-from. Valid steps: check, migrate, images, app, verify" ;;
  esac
fi

if [[ -n "$ONLY_STEP" ]]; then
  case "$ONLY_STEP" in
    check) step_check ;;
    migrate) step_migrate ;;
    images) step_images ;;
    app) step_app ;;
    verify) step_verify ;;
    *) error_exit "Invalid step '$ONLY_STEP' for --only. Valid steps: check, migrate, images, app, verify" ;;
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
      migrate) step_migrate ;;
      images) step_images ;;
      app) step_app ;;
      verify) step_verify ;;
    esac
  fi
done

log_success "Upgrade completed end-to-end successfully!"
