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
# XMC v2 Automated Deployment Script (deploy_xmc.sh)
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
  echo "                             Steps: compile, apis, primary, compute, gateway-api, prereqs, images, app, verify"
  echo "  --check-only              Run pre-flight dependency & project checks and exit"
  echo "  -h, --help                Show this help message"
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


# Safe YAML parameter extractor (dependency-free parsing using Python standard library)
get_config_val() {
  local key="$1"
  local regex="$2"
  local val

  # Read value using simple grep + cut + sed pipeline (consistent with xmc_functions.sh)
  val=$(grep -E "^[[:space:]]*${key}:" "$CONFIG_PATH" | head -n 1 | cut -d':' -f2- | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")

  if [[ -z "$val" ]]; then
    error_exit "Required configuration field '$key' is missing from '$CONFIG_PATH'."
  fi

  if [[ ! "$val" =~ $regex ]]; then
    error_exit "Field '$key' value '$val' is invalid. Must match regex: $regex"
  fi

  echo "$val"
}

# Helper to execute a deployment group using Cluster Toolkit (ghpc)
run_ghpc_deploy() {
  local group="$1"
  ./cluster-toolkit/ghpc deploy "ctk-out/$XMC_DEPLOYMENT_NAME" --only "$group" --auto-approve
}

# Generic waiter function with timeout
wait_for_condition() {
  local timeout="$1"
  local interval="$2"
  local check_func="$3"
  local err_msg="$4"

  local elapsed=0
  until $check_func; do
    if (( elapsed >= timeout )); then
      error_exit "$err_msg"
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
}

# Pre-flight Checks
run_checks() {
  log_info "Running pre-flight dependency checks..."

  # 1. Verify Binaries
  for cmd in python3 gcloud kubectl helm terraform bazel; do
    command -v "$cmd" >/dev/null 2>&1 || error_exit "CLI dependency '$cmd' is required but not installed."
  done

  local local_ghpc="./cluster-toolkit/ghpc"
  if [[ ! -f "$local_ghpc" || ! -x "$local_ghpc" ]]; then
    error_exit "Local cluster-toolkit binary '$local_ghpc' not found or is not executable."
  fi

  # 2. Check Terraform version (>= 1.12.2)
  local tf_version
  tf_version=$(terraform --version | head -n 1 | cut -d'v' -f2)
  if [[ "$(printf '%s\n%s' '1.12.2' "$tf_version" | sort -V | head -n1)" != "1.12.2" ]]; then
    error_exit "Terraform version must be >= 1.12.2. Found: $tf_version"
  fi

  # 3. Check active GCP Project alignment (Fail immediately on mismatch)
  local active_project
  active_project=$(gcloud config get-value project 2>/dev/null)
  if [[ "$active_project" != "$XMC_PROJECT_ID" ]]; then
    error_exit "Active gcloud project ('$active_project') does not match config project ('$XMC_PROJECT_ID'). Please switch project first."
  fi

  # 4. Check DNS project access (if automated)
  if [ "$USE_AUTOMATED_DNS" = true ]; then
    log_info "Verifying access to Cloud DNS project '$DNS_PROJECT' and zone '$DNS_ZONE'..."
    gcloud dns managed-zones describe "$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || \
      error_exit "Could not describe Cloud DNS zone '$DNS_ZONE' in project '$DNS_PROJECT'. Check your permissions."
  fi


  # 7. Verify terminal interactivity if manual DNS mode is selected
  if [ "$USE_AUTOMATED_DNS" = false ]; then
    if [ ! -t 0 ]; then
      error_exit "Script is running in a non-interactive terminal but manual DNS mode was selected. Cannot pause for user input. Please provide --dns-project and --dns-zone for automated DNS deployment."
    fi
  fi

  log_success "All pre-flight checks passed."
}

# Automate DNS Records Configuration (Fail if records exist)
apply_dns_records() {
  local ip="$1"
  log_info "Checking if records already exist in Cloud DNS project '$DNS_PROJECT', zone '$DNS_ZONE'..."
  
  # End domains with dot for DNS A-records
  local d_app="$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."
  local d_api="api.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."
  local d_grpc="grpc.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME."

  # Fetch all existing record names
  local existing
  existing=$(gcloud dns record-sets list --zone="$DNS_ZONE" --project="$DNS_PROJECT" --format="value(name)")
  
  # Fail if any record already exists to prevent accidental removal/override
  for subdomain in "$d_app" "$d_api" "$d_grpc"; do
    if echo "$existing" | grep -q "^$subdomain$"; then
      error_exit "DNS record for '$subdomain' already exists in zone '$DNS_ZONE'. Aborting to prevent accidental modifications. Please delete it manually first if you want to update it."
    fi
  done

  # Initialize transaction
  gcloud dns record-sets transaction abort --zone="$DNS_ZONE" --project="$DNS_PROJECT" >/dev/null 2>&1 || true
  gcloud dns record-sets transaction start --zone="$DNS_ZONE" --project="$DNS_PROJECT"

  for subdomain in "$d_app" "$d_api" "$d_grpc"; do
    log_info "Adding A record: $subdomain -> $ip"
    gcloud dns record-sets transaction add "$ip" --name="$subdomain" --ttl=300 --type=A --zone="$DNS_ZONE" --project="$DNS_PROJECT"
  done

  gcloud dns record-sets transaction execute --zone="$DNS_ZONE" --project="$DNS_PROJECT"
  log_success "Cloud DNS records successfully created."
}

# --- Steps implementation ---

step_compile() {
  log_info "Compiling XMC blueprint using GHPC..."
  # Clean and configure ctk-out with safe permissions
  mkdir -p ctk-out
  chmod 700 ctk-out
  ./cluster-toolkit/ghpc create deploy/ctk_v2/xmanager-cloud.yaml -d "$CONFIG_PATH" -o ctk-out -l WARNING -w --force
  log_success "Blueprint compiled successfully under 'ctk-out/'."
}

step_apis() {
  log_info "Deploying apis group (API enablement)..."
  run_ghpc_deploy apis
}

step_primary() {
  log_info "Deploying primary group (Network, Databases & certs)..."
  run_ghpc_deploy primary
  
  # Fetch IP
  local gw_ip_name="xmc-gateway-static-ip-$XMC_ENVIRONMENT"
  log_info "Extracting static Gateway IP ($gw_ip_name)..."
  GATEWAY_IP=$(gcloud compute addresses describe "$gw_ip_name" --global --project="$XMC_PROJECT_ID" --format='value(address)' 2>/dev/null || echo "")
  
  if [[ -z "$GATEWAY_IP" ]]; then
    error_exit "Failed to extract gateway IP address."
  fi
  
  log_success "Core Infrastructure deployed. Gateway IP: $GATEWAY_IP"

  # Perform DNS setup (automated or manual pause)
  if [ "$USE_AUTOMATED_DNS" = true ]; then
    apply_dns_records "$GATEWAY_IP"
  else
    echo -e "\n========================================================"
    echo -e "\033[1;33m[ACTION REQUIRED] Manual DNS & OAuth Configuration\033[0m"
    echo -e "========================================================"
    echo -e "1. Configure the following A records in your DNS provider for Gateway IP ${GATEWAY_IP}:"
    echo -e "   - \033[1m$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME\033[0m -> \033[1;32m$GATEWAY_IP\033[0m"
    echo -e "   - \033[1mapi.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME\033[0m -> \033[1;32m$GATEWAY_IP\033[0m"
    echo -e "   - \033[1mgrpc.$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME\033[0m -> \033[1;32m$GATEWAY_IP\033[0m"
    echo
    echo -e "2. Add the following Redirect URI to your GCP OAuth Client ID:"
    echo -e "   - \033[1mhttps://$XMC_ENVIRONMENT.$XMC_DOMAIN_NAME/api/callback\033[0m"
    echo -e "========================================================"
    read -p "Once DNS records are active and OAuth is configured, press [Enter] to continue..."
  fi
}

step_compute() {
  log_info "Deploying compute group (GKE Cluster and Node pools)..."
  run_ghpc_deploy compute

  local cluster_name="xmc-$XMC_DEPLOYMENT_NAME-$XMC_ENVIRONMENT"
  log_info "Fetching kubectl credentials for GKE cluster '$cluster_name'..."
  gcloud container clusters get-credentials "$cluster_name" \
      --region="$XMC_REGION" \
      --project="$XMC_PROJECT_ID"
}

step_gateway_api() {
  local cluster_name="xmc-$XMC_DEPLOYMENT_NAME-$XMC_ENVIRONMENT"
  log_info "Checking if GKE Gateway API is enabled on cluster '$cluster_name'..."
  
  local gw_enabled
  gw_enabled=$(gcloud container clusters describe "$cluster_name" --region="$XMC_REGION" --project="$XMC_PROJECT_ID" --format='value(gatewayConfig.enabled)' 2>/dev/null || echo "False")
  
  if [[ "$gw_enabled" != "True" ]]; then
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
  run_ghpc_deploy kubernetes_prereqs
}

step_images() {
  log_info "Configuring Docker registry authorization for GCR/AR..."
  gcloud auth configure-docker us-docker.pkg.dev --quiet

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
  run_ghpc_deploy xmc_app
}

step_verify() {
  log_info "Running diagnostics script..."
  ./deploy/ctk_v2/verify_xmc.sh || echo "Diagnostic script reported warnings."

  # Local checks for waiter
  check_gateway_programmed() {
    [[ "$(kubectl get gateway xmc-gateway -n xmc -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null)" == "True" ]]
  }
  check_cert_active() {
    [[ "$(gcloud certificate-manager certificates describe "xmc-cert-$XMC_ENVIRONMENT" --project="$XMC_PROJECT_ID" --format='value(managed.state)' 2>/dev/null)" == "ACTIVE" ]]
  }

  log_info "Verifying routing Gateway programmed state (5m timeout)..."
  wait_for_condition 300 10 check_gateway_programmed "Error: GKE Gateway failed to program within 300s. Run: kubectl describe gateway xmc-gateway -n xmc"
  log_success "GKE Gateway is Programmed."

  log_info "Verifying Certificate manager state (15m timeout)..."
  wait_for_condition 900 10 check_cert_active "Error: Managed Certificate failed to activate within 900s."
  log_success "Managed Certificate is ACTIVE."

  log_info "Verifying App Pod Health..."
  kubectl wait --namespace xmc --for=condition=ready pod --selector=app=ui --timeout=120s || {
    echo "Warning: App pods are not fully Ready. Checking for errors..."
    kubectl get pods -n xmc
    exit 1
  }

  log_success "XMC environment deployed and verified."
}

# --- Execution Entrypoint ---

# Parse arguments
CONFIG_PATH=""
START_STEP=""
CURRENT_STEP=""
DNS_PROJECT=""
DNS_ZONE=""
CHECK_ONLY=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config|-c)
      CONFIG_PATH="$2"
      shift 2
      ;;

    --start-from|-s)
      START_STEP="$2"
      shift 2
      ;;
    --dns-project)
      DNS_PROJECT="$2"
      shift 2
      ;;
    --dns-zone)
      DNS_ZONE="$2"
      shift 2
      ;;
    --check-only)
      CHECK_ONLY=true
      shift
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
  # Try to auto-discover configuration
  configs=(deploy/ctk_v2/*deployment.yaml)
  if [[ ${#configs[@]} -eq 1 && -f "${configs[0]}" ]]; then
    CONFIG_PATH="${configs[0]}"
    log_info "Auto-discovered deployment config: $CONFIG_PATH"
  else
    error_exit "Configuration file path (--config) is required."
  fi
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
  error_exit "Configuration file not found at '$CONFIG_PATH'."
fi

# DNS Parameters are optional but must be provided together if automated path is desired
USE_AUTOMATED_DNS=true
if [[ -z "$DNS_PROJECT" && -z "$DNS_ZONE" ]]; then
  USE_AUTOMATED_DNS=false
  log_info "DNS arguments omitted. Script will run in MANUAL DNS mode and pause for configuration."
elif [[ -z "$DNS_PROJECT" || -z "$DNS_ZONE" ]]; then
  error_exit "You must provide both --dns-project and --dns-zone for automated DNS, or omit both for manual DNS setup."
fi

# Load variables safely (using strict whitelist filters)
XMC_DEPLOYMENT_NAME=$(get_config_val "deployment_name" "^[a-zA-Z0-9_-]+$")
XMC_PROJECT_ID=$(get_config_val "project_id" "^[a-z0-9:-]+$")
XMC_ENVIRONMENT=$(get_config_val "environment" "^[a-zA-Z0-9_-]+$")
XMC_REGION=$(get_config_val "region" "^[a-z0-9-]+$")
XMC_DOMAIN_NAME=$(get_config_val "domain_name" "^[a-zA-Z0-9.-]+$")
export XMC_ACR_TARGET=$(get_config_val "xmc_acr_target" "^[a-zA-Z0-9./:-]+$")
XMC_IAP_CLIENT_ID=$(get_config_val "iap_client_id" "^[a-zA-Z0-9.-]+$")

# Run validations
run_checks

if [ "$CHECK_ONLY" = true ]; then
  log_success "Checks complete. Exiting (--check-only)."
  exit 0
fi

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
if [[ -n "$START_STEP" ]]; then
  valid_steps=("compile" "apis" "primary" "compute" "gateway-api" "prereqs" "images" "app" "verify")
  is_valid=false
  for s in "${valid_steps[@]}"; do
    if [[ "$START_STEP" == "$s" ]]; then
      is_valid=true
      break
    fi
  done
  if [ "$is_valid" = false ]; then
    error_exit "Invalid step name '$START_STEP'. Valid steps: ${valid_steps[*]}"
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
  if [ "$start_executing" = true ] || [ "$START_STEP" = "$name" ]; then
    start_executing=true
    CURRENT_STEP="$name"
    $func
  fi
}

run_step "compile"     step_compile
run_step "apis"        step_apis
run_step "primary"     step_primary
run_step "compute"     step_compute
run_step "gateway-api" step_gateway_api
run_step "prereqs"     step_prereqs
run_step "images"      step_images
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
