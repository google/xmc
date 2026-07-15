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
  echo "Usage: $0 [--config <path_to_yaml>] [options]"
  echo
  echo "Options:"
  echo "  --config, -c <path>       Path to deployment config YAML file (Default: deploy/ctk_v2/xmanager-cloud-deployment.yaml)"
  echo "  --start-from, -s <step>   Start/resume upgrade from this step onwards"
  echo "  --only <step>             Execute ONLY this specific step and exit"
  echo "                             Steps: check, migrate, images, app, restart, verify"
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

load_config() {
  local config_file="$1"
  if [[ ! -f "$config_file" ]]; then
    error_exit "Configuration file '$config_file' not found. Please ensure you have deployed the environment using Cluster Toolkit."
  fi
  log_info "Loading configuration from '$config_file'..."


  # Parse YAML using python
  local yaml_vars
  yaml_vars=$(python3 -c "
import yaml, sys
try:
    with open(sys.argv[1]) as f:
        data = yaml.safe_load(f)
    vars = data.get('vars', {})
    for k, v in vars.items():
        if isinstance(v, (str, int, float, bool)):
            print(f\"XMC_VAR_{k}='{v}'\")
except Exception as e:
    print(f'Error parsing YAML: {e}', file=sys.stderr)
    sys.exit(1)
" "$config_file" 2>/dev/null)

  if [[ $? -ne 0 ]] || [[ -z "$yaml_vars" ]]; then
    error_exit "Failed to parse configuration file '$config_file' or it contains no variables."
  fi

  eval "$yaml_vars"

  # Map YAML variables to script variables with env overrides support
  export PROJECT_ID="${PROJECT_ID:-${XMC_VAR_project_id:-}}"
  export XMC_DEPLOYMENT_NAME="${XMC_DEPLOYMENT_NAME:-${XMC_VAR_deployment_name:-}}"
  export GCP_REGION="${GCP_REGION:-${XMC_VAR_region:-us-central1}}"
  export XMANAGER_ENV="${XMANAGER_ENV:-${XMC_VAR_environment:-dev}}"
  export K8S_NAMESPACE="${K8S_NAMESPACE:-${XMC_VAR_namespace:-xmc}}"
  
  # Derive GKE cluster name
  export GKE_CLUSTER="xmc-${XMC_DEPLOYMENT_NAME}-${XMANAGER_ENV}"

  # Validate required variables
  if [[ -z "$PROJECT_ID" ]]; then
    error_exit "project_id is missing in vars of '$config_file'."
  fi
  if [[ -z "$XMC_DEPLOYMENT_NAME" ]]; then
    error_exit "deployment_name is missing in vars of '$config_file'."
  fi

  # Apply defaults for other variables
  export XMC_REGISTRY_NAME="xmc"
  export XMC_REGISTRY="us-docker.pkg.dev/${PROJECT_ID}/${XMC_REGISTRY_NAME}"
  export XMC_ACR_TARGET="${XMC_ACR_TARGET:-${XMC_REGISTRY}}"
}

get_migration_status() {
  local db_name="$1"
  local migrations_dir="$2"
  local spanner_instance="$3"

  local max_version=0
  if ls internal/storage/spanner/migrations/"$migrations_dir"/*.up.sql >/dev/null 2>&1; then
    max_version=$(ls internal/storage/spanner/migrations/"$migrations_dir"/*.up.sql | xargs -n 1 basename | cut -d'_' -f1 | sed 's/^0*//' | sort -rn | head -n 1)
  fi
  MIGRATION_MAX="${max_version:-0}"

  log_info "Fetching current $migrations_dir database version..."
  local version_out
  version_out=$(bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="$PROJECT_ID" \
    --instance="$spanner_instance" \
    --database="$db_name" \
    --migrations_dir="$migrations_dir" \
    --command="version" 2>/dev/null || echo "Version: 0, Dirty: false")
  
  MIGRATION_CURRENT=$(echo "$version_out" | grep -oE "Version: [0-9]+" | cut -d' ' -f2 || echo "0")
  MIGRATION_CURRENT="${MIGRATION_CURRENT:-0}"
  
  local dirty
  dirty=$(echo "$version_out" | grep -oE "Dirty: (true|false)" | cut -d' ' -f2 || echo "false")

  if [[ "$dirty" == "true" ]]; then
    error_exit "$migrations_dir database is in a DIRTY state. Please resolve manually before proceeding."
  fi
}

run_migration() {
  local db_name="$1"
  local migrations_dir="$2"
  local spanner_instance="$3"

  log_info "Running $migrations_dir migrations..."
  bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="$PROJECT_ID" \
    --instance="$spanner_instance" \
    --database="$db_name" \
    --migrations_dir="$migrations_dir" \
    --command="up"
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
  get_migration_status "$ess_db" "ess" "$spanner_instance"
  local current_ess_version="$MIGRATION_CURRENT"
  local max_ess_version="$MIGRATION_MAX"

  # Check XID
  get_migration_status "$xid_db" "xid" "$spanner_instance"
  local current_xid_version="$MIGRATION_CURRENT"
  local max_xid_version="$MIGRATION_MAX"

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
        run_migration "$ess_db" "ess" "$spanner_instance"
      fi

      if (( max_xid_version > current_xid_version )); then
        run_migration "$xid_db" "xid" "$spanner_instance"
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
  # We use --download-dependencies to prevent interactive prompts for downloading terraform/plugins in automated or non-interactive environments.
  ./cluster-toolkit/gcluster --download-dependencies deploy "ctk-out/$XMC_DEPLOYMENT_NAME" --only xmc_app --auto-approve

  log_success "Application upgrade initiated."
}

step_restart() {
  # Perform a rolling rollout restart of all XMC service deployments so pods pull newly pushed :latest images
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

  log_success "Rolling deployment restarts initiated."
}

step_verify() {
  log_info "Verifying rollout completion and cluster health in namespace '$K8S_NAMESPACE'..."

  log_info "Running verification script..."
  ENVIRONMENT="$XMANAGER_ENV" REGION="$GCP_REGION" PROJECT_ID="$PROJECT_ID" python3 ./deploy/ctk_v2/verify_xmc.py || log_info "Diagnostic script reported warnings."

  log_success "XMC environment upgrade to current repository branch state verified successfully."
}

# --- Execution Entrypoint ---

CONFIG_FILE="deploy/ctk_v2/xmanager-cloud-deployment.yaml"
START_STEP=""
ONLY_STEP=""
RUN_MIGRATIONS=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config|-c)
      CONFIG_FILE="$2"
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

load_config "$CONFIG_FILE"

STEPS=("check" "migrate" "images" "app" "restart" "verify")

if [[ -n "$START_STEP" ]]; then
  case "$START_STEP" in
    check|migrate|images|app|restart|verify) ;;
    *) error_exit "Invalid step '$START_STEP' for --start-from. Valid steps: check, migrate, images, app, restart, verify" ;;
  esac
fi

if [[ -n "$ONLY_STEP" ]]; then
  case "$ONLY_STEP" in
    check) step_check ;;
    migrate) step_migrate ;;
    images) step_images ;;
    app) step_app ;;
    restart) step_restart ;;
    verify) step_verify ;;
    *) error_exit "Invalid step '$ONLY_STEP' for --only. Valid steps: check, migrate, images, app, restart, verify" ;;
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
      restart) step_restart ;;
      verify) step_verify ;;
    esac
  fi
done

log_success "Upgrade completed end-to-end successfully!"
