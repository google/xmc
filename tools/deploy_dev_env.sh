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
# XMC v2 Dev Environment Deployment Script (deploy_dev_env.sh)
# ==============================================================================
#
# Minimum Required IAM Roles for Deploying Identity:
#   - VPC & Static IP: roles/compute.networkAdmin
#   - GKE & Gateway API: roles/container.admin
#   - Spanner & Schema: roles/spanner.admin
#   - Image Registry: roles/artifactregistry.writer
#   - Certificate Manager: roles/certificatemanager.owner
#   - Workload Identity Bindings: roles/iam.serviceAccountAdmin, roles/resourcemanager.projectIamAdmin
#   - DNS Automation: roles/dns.admin (on the DNS project)
# ==============================================================================

set -Eeo pipefail

# --- Help Menu ---
show_help() {
  echo "Usage: $0 --config <deployment_yaml_path> --dns-project <dns_project> --dns-zone <dns_zone> [options]"
  echo
  echo "Options:"
  echo "  --config, -c <path>       Path to XMC deployment YAML configuration file (Required)"
  echo "  --dns-project <project>   GCP Project ID hosting the DNS zone (Required)"
  echo "  --dns-zone <zone_name>    Cloud DNS Managed Zone name (Required)"
  echo "  --start-from, -s <step>   Start/resume deployment from this step onwards"
  echo "  --only <step>             Execute ONLY this specific step and exit"
  echo "                             Steps: check, compile, images, apis, primary, compute, gateway-api, prereqs, app, verify"
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


# Safe YAML parameter extractor using grep, cut, and sed
get_config_val() {
  local key="$1"
  local regex="$2"
  local val

  # Read value using simple grep + cut + sed pipeline
  val=$(grep -E "^[[:space:]]*${key}:" "$CONFIG_PATH" | head -n 1 | cut -d':' -f2- | sed -e 's/#.*$//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")

  if [[ -z "$val" ]]; then
    error_exit "Required configuration field '$key' is missing from '$CONFIG_PATH'."
  fi

  if [[ ! "$val" =~ $regex ]]; then
    error_exit "Field '$key' value '$val' is invalid. Must match regex: $regex"
  fi

  echo "$val"
}

# Helper to execute a deployment group using Cluster Toolkit (gcluster)
run_gcluster_deploy() {
  local group="$1"
  ./cluster-toolkit/gcluster deploy "ctk-out/$XMC_DEPLOYMENT_NAME" --only "$group" --auto-approve
}

# Pre-flight Checks
run_checks() {
  log_info "Running pre-flight dependency checks..."
  local cmd

  # 1. Verify Binaries
  for cmd in python3 gcloud kubectl helm terraform bazel; do
    command -v "$cmd" >/dev/null 2>&1 || error_exit "CLI dependency '$cmd' is required but not installed."
  done

  local local_gcluster="./cluster-toolkit/gcluster"
  if [[ ! -f "$local_gcluster" || ! -x "$local_gcluster" ]]; then
    error_exit "Local cluster-toolkit binary '$local_gcluster' not found or is not executable."
  fi

  # 2. Check Terraform version (>= 1.12.2)
  local tf_version
  tf_version=$(terraform --version | sed -n '1p' | cut -d'v' -f2)
  if ! python3 -c '
import sys, re
def parse_version(v):
    return tuple(map(int, re.findall(r"\d+", v)[:3]))
sys.exit(0 if parse_version(sys.argv[1]) >= parse_version(sys.argv[2]) else 1)
' "$tf_version" "1.12.2"; then
    error_exit "Terraform version must be >= 1.12.2. Found: $tf_version"
  fi

  # 3. Check active GCP Project alignment (Fail immediately on mismatch)
  local active_project
  active_project=$(gcloud config get-value project 2>/dev/null)
  if [[ "$active_project" != "$XMC_PROJECT_ID" ]]; then
    error_exit "Active gcloud project ('$active_project') does not match config project ('$XMC_PROJECT_ID'). Please switch project first."
  fi

  # 4. Check DNS project access
  log_info "Verifying access to Cloud DNS project '$DNS_PROJECT' and zone '$DNS_ZONE'..."
  gcloud dns managed-zones describe "$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || \
    error_exit "Could not describe Cloud DNS zone '$DNS_ZONE' in project '$DNS_PROJECT'. Check your permissions."

  log_success "All pre-flight checks passed."
}

# Automate DNS Records Configuration (skips if pointing to correct IP, fails on conflict)
apply_dns_records() {
  local ip="$1"
  log_info "Verifying Cloud DNS record states in project '$DNS_PROJECT', zone '$DNS_ZONE'..."
  local subdomain
  
  # End domains with dot for DNS A-records
  local d_app="$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."
  local d_api="api.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."
  local d_grpc="grpc.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."

  # Initialize transaction
  gcloud dns record-sets transaction abort --zone="$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || true
  gcloud dns record-sets transaction start --zone="$DNS_ZONE" --project="$DNS_PROJECT"

  local has_transaction_changes=false

  for subdomain in "$d_app" "$d_api" "$d_grpc"; do
    local existing_ip
    existing_ip=$(gcloud dns record-sets list --zone="$DNS_ZONE" --project="$DNS_PROJECT" --name="$subdomain" --type=A --format="value(rrdatas)" 2>/dev/null || echo "")
    
    if [[ "$existing_ip" == "$ip" ]]; then
      log_info "DNS record for '$subdomain' already points to $ip. Skipping."
    elif [[ -n "$existing_ip" ]]; then
      gcloud dns record-sets transaction abort --zone="$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || true
      error_exit "DNS record for '$subdomain' already exists but points to a different IP ($existing_ip). Aborting to prevent accidental conflict. Please delete it manually first if you want to update it to $ip."
    else
      log_info "Creating new DNS record: $subdomain -> $ip"
      gcloud dns record-sets transaction add "$ip" --name="$subdomain" --ttl=300 --type=A --zone="$DNS_ZONE" --project="$DNS_PROJECT"
      has_transaction_changes=true
    fi
  done

  if [[ "$has_transaction_changes" == "true" ]]; then
    gcloud dns record-sets transaction execute --zone="$DNS_ZONE" --project="$DNS_PROJECT"
    log_success "Cloud DNS records successfully updated."
  else
    # Clean up transaction file since no changes were made
    gcloud dns record-sets transaction abort --zone="$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || true
    log_success "Cloud DNS records are already up to date."
  fi
}

# --- Steps implementation ---

step_check() {
  log_success "Pre-flight checks verified."
}

step_compile() {
  log_info "Compiling XMC blueprint using gcluster..."
  ./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d "$CONFIG_PATH" -o ctk-out -l WARNING -w --force
  log_success "Blueprint compiled successfully under 'ctk-out/'."
}

step_apis() {
  log_info "Deploying apis group (API enablement)..."
  run_gcluster_deploy apis
  log_success "APIs enabled successfully."
}

step_primary() {
  log_info "Deploying primary group (Network, Databases & certs)..."
  run_gcluster_deploy primary
  
  # Fetch IP
  local gw_ip_name="xmc-gateway-static-ip-$XMC_ENVIRONMENT"
  local GATEWAY_IP
  log_info "Extracting static Gateway IP ($gw_ip_name)..."
  GATEWAY_IP=$(gcloud compute addresses describe "$gw_ip_name" --global --project="$XMC_PROJECT_ID" --format='value(address)')
  
  if [[ -z "$GATEWAY_IP" ]]; then
    error_exit "Failed to extract gateway IP address."
  fi
  
  log_success "Core Infrastructure deployed. Gateway IP: $GATEWAY_IP"

  # Perform DNS setup
  apply_dns_records "$GATEWAY_IP"
}

step_compute() {
  log_info "Deploying compute group (GKE Cluster and Node pools)..."
  run_gcluster_deploy compute
  log_success "Compute group (GKE cluster and Node pools) deployed successfully."
}

step_gateway_api() {
  local cluster_name="xmc-$XMC_DEPLOYMENT_NAME-$XMC_ENVIRONMENT"
  log_info "Checking if GKE Gateway API is enabled on cluster '$cluster_name'..."
  
  local gw_enabled
  gw_enabled=$(gcloud container clusters describe "$cluster_name" --region="$XMC_REGION" --project="$XMC_PROJECT_ID" --format='value(gatewayConfig.enabled)')
  
  if [[ "${gw_enabled,,}" != "true" ]]; then
    log_info "Enabling GKE Gateway API (standard channel)..."
    gcloud container clusters update "$cluster_name" \
        --gateway-api=standard \
        --region="$XMC_REGION" \
        --project="$XMC_PROJECT_ID"
    log_success "Gateway API enabled."
  else
    log_info "Gateway API standard channel is already enabled."
  fi
}

step_prereqs() {
  log_info "Deploying kubernetes_prereqs group (cert-manager, Kueue, Jobset)..."
  run_gcluster_deploy kubernetes_prereqs
  log_success "Kubernetes prerequisites (cert-manager, Kueue, Jobset) deployed successfully."
}

step_images() {
  log_info "Configuring Docker registry authorization for registry..."
  local registry_host
  registry_host=$(echo "$XMC_ACR_TARGET" | cut -d'/' -f1)
  if [[ "$registry_host" =~ gcr\.io$ || "$registry_host" =~ \.pkg\.dev$ ]]; then
    gcloud auth configure-docker "$registry_host" --quiet
  else
    log_info "Non-GCP registry detected ($registry_host). Skipping gcloud credential helper."
  fi

  log_info "Building and pushing XMC Service Images using registry $XMC_ACR_TARGET..."
  # XMC_ACR_TARGET is retrieved from environment as verified in checks
  export XMC_ENV="$XMC_ENVIRONMENT"

  log_info "Pushing actuator..."
  bazel run //cmd/actuator:actuator_image_push
  log_info "Pushing experiment_state_server..."
  bazel run //cmd/experiment_state_server:experiment_state_server_image_push
  log_info "Pushing log_monitor..."
  bazel run //cmd/log_monitor:log_monitor_image_push
  log_info "Pushing reconciler..."
  bazel run //cmd/reconciler:reconciler_image_push
  log_info "Pushing ui..."
  bazel run //cmd/ui:ui_image_push
  log_info "Pushing xid_service..."
  bazel run //cmd/xid_service:xid_service_image_push
  
  log_success "All images built and pushed."
}

step_app() {
  log_info "Deploying xmc_app group (Helm release)..."
  run_gcluster_deploy xmc_app
  log_success "XMC application Helm release deployed successfully."
}

step_verify() {
  local cluster_name="xmc-$XMC_DEPLOYMENT_NAME-$XMC_ENVIRONMENT"
  log_info "Fetching kubectl credentials for GKE cluster '$cluster_name'..."
  gcloud container clusters get-credentials "$cluster_name" \
      --region="$XMC_REGION" \
      --project="$XMC_PROJECT_ID"

  log_info "Running verification script..."
  ENVIRONMENT="$XMC_ENVIRONMENT" REGION="$XMC_REGION" PROJECT_ID="$XMC_PROJECT_ID" bash ./deploy/ctk_v2/verify_xmc.sh || echo "Diagnostic script reported warnings."

  log_info "Verifying routing Gateway programmed state (5m timeout)..."
  kubectl wait --for=condition=Programmed gateway/xmc-gateway -n xmc --timeout=300s || \
    error_exit "GKE Gateway failed to program within 300s. Run: kubectl describe gateway xmc-gateway -n xmc"
  log_success "GKE Gateway is Programmed."

  log_info "Verifying Certificate manager state (15m timeout)..."
  local cert_state=""
  for ((i=1; i<=90; i++)); do
    cert_state=$(gcloud certificate-manager certificates describe "xmc-cert-$XMC_ENVIRONMENT" --project="$XMC_PROJECT_ID" --format='value(managed.state)' 2>/dev/null || echo "PENDING")
    log_info "Checking certificate status (attempt $i/90): $cert_state"
    [[ "$cert_state" == "ACTIVE" ]] && break
    sleep 10
  done
  [[ "$cert_state" == "ACTIVE" ]] || error_exit "Managed Certificate failed to activate within 900s."
  log_success "Managed Certificate is ACTIVE."

  log_success "XMC environment deployed and verified."
}

# --- Execution Entrypoint ---

# Parse arguments
CONFIG_PATH=""
START_STEP=""
ONLY_STEP=""
CURRENT_STEP=""
DNS_PROJECT=""
DNS_ZONE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config|-c)
      [[ $# -lt 2 ]] && error_exit "Option $1 requires an argument."
      CONFIG_PATH="$2"
      shift 2
      ;;

    --start-from|-s)
      [[ $# -lt 2 ]] && error_exit "Option $1 requires an argument."
      START_STEP="$2"
      shift 2
      ;;
    --only)
      [[ $# -lt 2 ]] && error_exit "Option $1 requires an argument."
      ONLY_STEP="$2"
      shift 2
      ;;
    --dns-project)
      [[ $# -lt 2 ]] && error_exit "Option $1 requires an argument."
      DNS_PROJECT="$2"
      shift 2
      ;;
    --dns-zone)
      [[ $# -lt 2 ]] && error_exit "Option $1 requires an argument."
      DNS_ZONE="$2"
      shift 2
      ;;
    -h|--help)
      show_help
      ;;
    *)
      error_exit "Unknown option: $1"
      ;;
  esac
done

if [[ -z "$CONFIG_PATH" ]]; then
  error_exit "Configuration file path (--config) is required."
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
  error_exit "Configuration file not found at '$CONFIG_PATH'."
fi

if [[ -z "$DNS_PROJECT" || -z "$DNS_ZONE" ]]; then
  error_exit "You must provide both --dns-project and --dns-zone. Automated DNS configuration is required in this developer environment script."
fi

if [[ -n "$START_STEP" && -n "$ONLY_STEP" ]]; then
  error_exit "Cannot specify both --start-from and --only."
fi

# Load variables safely (using strict whitelist filters)
XMC_DEPLOYMENT_NAME=$(get_config_val "deployment_name" "^[a-zA-Z0-9_-]+$")
XMC_PROJECT_ID=$(get_config_val "project_id" "^[a-z0-9:.-]+$")
XMC_ENVIRONMENT=$(get_config_val "environment" "^[a-zA-Z0-9_-]+$")
XMC_REGION=$(get_config_val "region" "^[a-z0-9-]+$")
XMC_DOMAIN_NAME=$(get_config_val "domain_name" "^[a-zA-Z0-9.-]+$")
export XMC_ACR_TARGET=$(get_config_val "xmc_acr_target" "^[a-zA-Z0-9./:-]+$")

# Run validations
run_checks

# Trap custom errors to show which line/command crashed
on_error() {
  local parent_lineno="$1"
  local code="$2"
  echo -e "\n\033[0;31m[ERROR] Command failed at line ${parent_lineno} with exit code ${code}.\033[0m" >&2
  if [[ -n "$CURRENT_STEP" ]]; then
    echo "You can resume from this step using:"
    local dns_args=""
    if [[ -n "$DNS_PROJECT" && -n "$DNS_ZONE" ]]; then
      dns_args="--dns-project $DNS_PROJECT --dns-zone $DNS_ZONE "
    fi
    echo "  $0 --config $CONFIG_PATH ${dns_args}--start-from $CURRENT_STEP"
  fi
  exit 1
}
trap 'on_error ${LINENO} $?' ERR

# Validate step name if provided
if [[ -n "$START_STEP" || -n "$ONLY_STEP" ]]; then
  step_to_validate="${START_STEP:-$ONLY_STEP}"
  if [[ ! " check compile images apis primary compute gateway-api prereqs app verify " =~ " ${step_to_validate} " ]]; then
    error_exit "Invalid step name '$step_to_validate'. Valid steps: check, compile, images, apis, primary, compute, gateway-api, prereqs, app, verify"
  fi
fi

# Execute steps sequentially (start from START_STEP if specified, otherwise run all)
start_executing=false
if [[ -z "$START_STEP" ]]; then
  start_executing=true
fi

run_step() {
  local name="$1"
  local func="$2"
  if [[ -n "$ONLY_STEP" ]]; then
    if [[ "$ONLY_STEP" == "$name" ]]; then
      CURRENT_STEP="$name"
      $func
    fi
  else
    if [[ "$start_executing" == "true" || "$START_STEP" == "$name" ]]; then
      start_executing=true
      CURRENT_STEP="$name"
      $func
    fi
  fi
}

run_step "check"       step_check
run_step "compile"     step_compile
run_step "images"      step_images
run_step "apis"        step_apis
run_step "primary"     step_primary
run_step "compute"     step_compute
run_step "gateway-api" step_gateway_api
run_step "prereqs"     step_prereqs
run_step "app"         step_app
run_step "verify"      step_verify

# Print success message (only reached if all executed steps succeeded)
echo -e "\n========================================================"
echo -e "\033[0;32mDeployment Completed Successfully!\033[0m"
echo -e "========================================================"
echo -e "Final URL: \033[1mhttps://$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME\033[0m"
echo
echo -e "\033[1mPost-Deployment Action Required (OAuth Setup):\033[0m"
echo -e "Add this redirect URI in your GCloud Console OAuth Client ID config:"
echo -e "  - \033[1mhttps://$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME/api/callback\033[0m"
echo -e "========================================================"
