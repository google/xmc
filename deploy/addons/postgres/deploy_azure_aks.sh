#!/usr/bin/env bash
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

# Provisions an AKS cluster and deploys XMC on it with the PostgreSQL backend and no GCP
# dependencies ("island mode"): in-cluster PostgreSQL 16, Redis, Dex OIDC, JobSet and Kueue, and
# the XMC chart with database.backend=postgres. The Azure counterpart of deploy_aws_eks.sh; like it,
# it mirrors the KinD dev flow (tools/dev_env) and the e2e auth values (test/e2e/lib/helm_deployer.py).
#
# Usage:
#   deploy/addons/postgres/deploy_azure_aks.sh [step...]
#   steps (default: all, in order): cluster controllers images postgres support xmc verify
#   extra steps:
#     expose    publishes the gateway through an Azure Standard Load Balancer (public IP with a DNS
#               label, plain HTTP, IP-allowlisted) and re-points Dex and the chart at it
#     e2e       runs the Playwright suite (test/e2e/test_e2e_flow.py) against the public URL
#     teardown  deletes the resource group: cluster, registry, node resource group and public IP
#
# Environment:
#   AZURE_TENANT_ID            Tenant for the device-code login when the CLI is not logged in.
#   AZURE_SUBSCRIPTION         Subscription name or ID (default: the CLI's current subscription).
#   AZURE_LOCATION             Region (default: swedencentral).
#   OWNER_TAG                  Value of the `user`/`owner` tags on every Azure resource (default: $USER).
#   RESOURCE_GROUP             Resource group (default: <owner>-xmc); AKS puts the node resources in
#                              <resource group>-nodes.
#   CLUSTER_NAME               AKS cluster name (default: xmc-<owner>).
#   AKS_VERSION                Kubernetes version (default: the region's AKS default).
#   KUBECONFIG_FILE            Kubeconfig the script writes and uses (default:
#                              ~/.kube/<cluster>.aks.yaml), which leaves the current context of
#                              ~/.kube/config alone for tools that rely on it.
#   NODE_VM_SIZE / NODE_COUNT  Node pool shape (default: Standard_D4s_v5 x 3, the size of m6i.xlarge).
#   ACR_NAME                   Container registry name (default: xmc<owner>; letters and digits only).
#   ACR_PULL                   How the nodes pull from the registry: "attach" grants the kubelet
#                              identity AcrPull (needs Owner, User Access Administrator or Role Based
#                              Access Control Administrator), "secret" uses a pull-only registry token
#                              as an imagePullSecret, "auto" (default) picks "attach" when the caller
#                              may assign roles and "secret" otherwise (e.g. as Contributor).
#   JOBSET_CHART_VERSION       JobSet OCI chart (default: 0.12.0, as deploy/ctk_v2).
#   KUEUE_CHART_VERSION        Kueue OCI chart (default: 0.18.2, as deploy/ctk_v2; the chart's
#                              kueue-dev queues need the v1beta2 API).
#   NAMESPACE                  XMC namespace (default: xmc).
#   ALLOWED_CIDRS              "expose" only: comma-separated client CIDRs allowed through the load
#                              balancer (default: this machine's public IP /32).
#   DNS_LABEL                  "expose" only: DNS label of the public IP (default: the cluster name),
#                              which serves http://<label>.<location>.cloudapp.azure.com.
#   PUBLIC_URL                 Set automatically by "expose" (and found again by "e2e" and "xmc").
#   E2E_VENV                   "e2e" only: virtualenv with Playwright (default: ~/.cache/xmc-e2e-venv).
#   E2E_SCREENSHOTS_DIR        "e2e" only: screenshots and logs (default: <work dir>/e2e).
#
# Afterwards, from any machine with kubectl access to the cluster (az aks get-credentials):
#   kubectl port-forward -n xmc svc/gateway-proxy-service 8080:8080 &   # UI + REST at http://localhost:8080
#   kubectl port-forward -n dex svc/dex 5556:5556 &                     # Dex (login: admin@xmc.local / password123)
#   kubectl port-forward -n xmc svc/grpc-service 50051:50051 &          # ESS gRPC for the xmanager client
#   export XMANAGER_ENDPOINT=http://127.0.0.1:50051 XMC_DOMAIN=localhost:8080 XMANAGER_UI_URL=http://localhost:8080
#   xmanager login   # or XMC_AUTH_TOKEN=<Dex id_token> for non-interactive use
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
export PATH="${HOME}/.local/bin:${PATH}"

AZURE_LOCATION="${AZURE_LOCATION:-swedencentral}"
OWNER_TAG="${OWNER_TAG:-${USER:-xmc}}"
RESOURCE_GROUP="${RESOURCE_GROUP:-${OWNER_TAG}-xmc}"
CLUSTER_NAME="${CLUSTER_NAME:-xmc-${OWNER_TAG}}"
AKS_VERSION="${AKS_VERSION:-}"
KUBECONFIG_FILE="${KUBECONFIG_FILE:-${HOME}/.kube/${CLUSTER_NAME}.aks.yaml}"
export KUBECONFIG="${KUBECONFIG_FILE}"
NODE_VM_SIZE="${NODE_VM_SIZE:-Standard_D4s_v5}"
NODE_COUNT="${NODE_COUNT:-3}"
ACR_NAME="${ACR_NAME:-xmc${OWNER_TAG//[^a-zA-Z0-9]/}}"
ACR_PULL="${ACR_PULL:-auto}"
JOBSET_CHART_VERSION="${JOBSET_CHART_VERSION:-0.12.0}"
KUEUE_CHART_VERSION="${KUEUE_CHART_VERSION:-0.18.2}"
NAMESPACE="${NAMESPACE:-xmc}"
WORK_DIR="${WORK_DIR:-$(mktemp -d)}"
TAGS=("user=${OWNER_TAG}" "owner=${OWNER_TAG}" "purpose=xmc-postgres-island")
# Registry token and imagePullSecret used when ACR_PULL resolves to "secret".
PULL_TOKEN="xmc-pull"
PULL_SECRET="acr-pull"

log() { echo; echo "=== $* ==="; }

require_azure_login() {
  if ! az account show >/dev/null 2>&1; then
    echo "Azure CLI not logged in; starting device-code login..."
    az login --use-device-code ${AZURE_TENANT_ID:+--tenant "${AZURE_TENANT_ID}"} >/dev/null
  fi
  if [[ -n "${AZURE_SUBSCRIPTION:-}" ]]; then
    az account set --subscription "${AZURE_SUBSCRIPTION}"
  fi
  ACR_LOGIN_SERVER="${ACR_NAME}.azurecr.io"
  echo "Azure subscription $(az account show --query name -o tsv) ($(az account show --query id -o tsv)), location ${AZURE_LOCATION}, cluster ${CLUSTER_NAME}"
}

step_cluster() {
  log "cluster: AKS ${CLUSTER_NAME} (${AKS_VERSION:-default version}, ${NODE_COUNT}x ${NODE_VM_SIZE})"
  az group create -n "${RESOURCE_GROUP}" -l "${AZURE_LOCATION}" --tags "${TAGS[@]}" -o none
  if az aks show -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" -o none 2>/dev/null; then
    echo "cluster exists; waiting until it is provisioned"
    az aks wait -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" --created --interval 20 --timeout 1800
  else
    # Managed identity, Azure CNI Overlay and a Standard Load Balancer are the AKS defaults.
    az aks create -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" -l "${AZURE_LOCATION}" \
      ${AKS_VERSION:+--kubernetes-version "${AKS_VERSION}"} \
      --nodepool-name cpupool --node-count "${NODE_COUNT}" --node-vm-size "${NODE_VM_SIZE}" \
      --node-osdisk-size 80 --tier free --no-ssh-key \
      --node-resource-group "${RESOURCE_GROUP}-nodes" \
      --tags "${TAGS[@]}" --nodepool-tags "user=${OWNER_TAG}" "owner=${OWNER_TAG}" -o none
  fi
  mkdir -p "$(dirname "${KUBECONFIG_FILE}")"
  az aks get-credentials -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" --file "${KUBECONFIG_FILE}" \
    --overwrite-existing -o none
  kubectl get nodes
  kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  # AKS ships "default" (Azure Disk CSI) as the default StorageClass, which the Postgres PVC uses.
}

step_controllers() {
  log "controllers: JobSet ${JOBSET_CHART_VERSION} + Kueue ${KUEUE_CHART_VERSION}"
  helm upgrade --install jobset oci://registry.k8s.io/jobset/charts/jobset --version "${JOBSET_CHART_VERSION}" \
    -n jobset-system --create-namespace --wait --timeout 5m
  helm upgrade --install kueue oci://registry.k8s.io/kueue/charts/kueue --version "${KUEUE_CHART_VERSION}" \
    -n kueue-system --create-namespace --wait --timeout 5m
  kubectl -n jobset-system rollout status deployment/jobset-controller --timeout=180s
  kubectl -n kueue-system rollout status deployment/kueue-controller-manager --timeout=180s
}

step_images() {
  log "images: build with Bazel and push to ${ACR_LOGIN_SERVER}/xmc/*"
  if ! az acr show -n "${ACR_NAME}" -g "${RESOURCE_GROUP}" -o none 2>/dev/null; then
    az acr create -n "${ACR_NAME}" -g "${RESOURCE_GROUP}" -l "${AZURE_LOCATION}" --sku Basic \
      --tags "${TAGS[@]}" -o none
  fi
  az acr login -n "${ACR_NAME}"
  (cd "${REPO_ROOT}" && bazel build --config=linux_amd64 \
    //cmd/experiment_state_server:experiment_state_server_image_tarball \
    //cmd/actuator:actuator_image_tarball \
    //cmd/reconciler:reconciler_image_tarball \
    //cmd/xid_service:xid_service_image_tarball \
    //cmd/dashboard_service:dashboard_service_image_tarball \
    //cmd/health_service:health_service_image_tarball \
    //cmd/xmc_bridge:xmc_bridge_image_tarball \
    //internal/storage/postgres/migrations:postgres_migrator_image_tarball \
    && XMC_UI_PLATFORM=linux/amd64 bazel build //cmd/ui:ui_image_tarball)
  local -A tarballs=(
    ["experiment-state-server"]="cmd/experiment_state_server/experiment_state_server_image_tarball.tar"
    ["actuator"]="cmd/actuator/actuator_image_tarball.tar"
    ["reconciler"]="cmd/reconciler/reconciler_image_tarball.tar"
    ["xid-service"]="cmd/xid_service/xid_service_image_tarball.tar"
    ["dashboard-service"]="cmd/dashboard_service/dashboard_service_image_tarball.tar"
    ["health-service"]="cmd/health_service/health_service_image_tarball.tar"
    ["xmc-bridge"]="cmd/xmc_bridge/xmc_bridge_image_tarball.tar"
    ["postgres-migrator"]="internal/storage/postgres/migrations/postgres_migrator_image_tarball.tar"
    ["ui"]="cmd/ui/ui_image_tarball.tar"
  )
  local svc loaded
  for svc in "${!tarballs[@]}"; do
    loaded="$(docker load -i "${REPO_ROOT}/bazel-bin/${tarballs[${svc}]}" | sed -n 's/^Loaded image: //p' | tail -1)"
    docker tag "${loaded}" "${ACR_LOGIN_SERVER}/xmc/${svc}:latest"
    docker push "${ACR_LOGIN_SERVER}/xmc/${svc}:latest" | tail -1
  done
  grant_acr_pull
}

can_assign_roles() {
  local oid roles
  oid="$(az ad signed-in-user show --query id -o tsv 2>/dev/null)" || return 1
  roles="$(az role assignment list --assignee "${oid}" --all --include-inherited --include-groups \
    --query "[?roleDefinitionName=='Owner' || roleDefinitionName=='User Access Administrator' || roleDefinitionName=='Role Based Access Control Administrator'].roleDefinitionName" \
    -o tsv 2>/dev/null)" || return 1
  [[ -n "${roles}" ]]
}

grant_acr_pull() {
  local mode="${ACR_PULL}"
  if [[ "${mode}" == "auto" ]]; then
    if can_assign_roles; then mode="attach"; else mode="secret"; fi
  fi
  if [[ "${mode}" == "attach" ]]; then
    echo "registry access: AcrPull role for the kubelet identity"
    az aks update -g "${RESOURCE_GROUP}" -n "${CLUSTER_NAME}" --attach-acr "${ACR_NAME}" -o none
    kubectl delete secret "${PULL_SECRET}" -n "${NAMESPACE}" --ignore-not-found >/dev/null
    return
  fi
  # Without Microsoft.Authorization/roleAssignments/write (e.g. Contributor) the kubelet identity
  # cannot get AcrPull, so pods pull with a pull-only registry token instead. The chart has no
  # imagePullSecrets value: step_xmc adds the secret to its pod templates with a Helm post-renderer,
  # and the default service account carries it for the Postgres migration Jobs.
  echo "registry access: pull-only token '${PULL_TOKEN}' as imagePullSecret '${PULL_SECRET}'"
  local user="${PULL_TOKEN}" password=""
  if az acr token show -n "${PULL_TOKEN}" -r "${ACR_NAME}" -o none 2>/dev/null; then
    password="$(az acr token credential generate -n "${PULL_TOKEN}" -r "${ACR_NAME}" --password1 \
      --query 'passwords[0].value' -o tsv)"
  else
    password="$(az acr token create -n "${PULL_TOKEN}" -r "${ACR_NAME}" --scope-map _repositories_pull \
      --query 'credentials.passwords[0].value' -o tsv)" || password=""
  fi
  if [[ -z "${password}" ]]; then
    echo "registry tokens unavailable; falling back to the registry admin user"
    az acr update -n "${ACR_NAME}" --admin-enabled true -o none
    user="${ACR_NAME}"
    password="$(az acr credential show -n "${ACR_NAME}" --query 'passwords[0].value' -o tsv)"
  fi
  kubectl create secret docker-registry "${PULL_SECRET}" -n "${NAMESPACE}" \
    --docker-server="${ACR_LOGIN_SERVER}" --docker-username="${user}" --docker-password="${password}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl patch serviceaccount default -n "${NAMESPACE}" \
    -p "{\"imagePullSecrets\":[{\"name\":\"${PULL_SECRET}\"}]}" >/dev/null
}

step_postgres() {
  log "postgres: in-cluster PostgreSQL 16 + ESS/XID migrations"
  XMC_NAMESPACE="${NAMESPACE}" MIGRATOR_IMAGE="${ACR_LOGIN_SERVER}/xmc/postgres-migrator:latest" \
    "${SCRIPT_DIR}/setup_postgres_addon.sh"
}

step_support() {
  log "support: Redis, Dex, gateway proxy, dev secrets"
  kubectl apply -f "${REPO_ROOT}/test/e2e/manifests/redis.yaml"
  kubectl apply -f "${REPO_ROOT}/test/e2e/manifests/dex.yaml"
  # The KinD proxy config hard-codes KinD's cluster DNS (10.96.0.10); point nginx at this cluster's.
  local dns_ip
  dns_ip="$(kubectl get svc kube-dns -n kube-system -o jsonpath='{.spec.clusterIP}')"
  sed "s/resolver 10.96.0.10/resolver ${dns_ip}/" "${REPO_ROOT}/test/e2e/manifests/gateway_proxy.yaml" | kubectl apply -f -

  # The services build Google API clients (Cloud Identity for group ACL checks) at startup even
  # though nothing on this cluster calls Google. A parse-valid authorized_user file keeps them from
  # failing on load; group lookups would fail at request time and are skipped with a warning.
  cat > "${WORK_DIR}/key.json" <<'EOF'
{"type": "authorized_user", "client_id": "xmc-island-dummy.apps.googleusercontent.com", "client_secret": "xmc-island-dummy-secret", "refresh_token": "xmc-island-dummy-refresh-token", "quota_project_id": "xmc-island"}
EOF
  openssl genpkey -algorithm RSA -out "${WORK_DIR}/tls.key" -pkeyopt rsa_keygen_bits:2048 2>/dev/null
  openssl req -new -x509 -key "${WORK_DIR}/tls.key" -out "${WORK_DIR}/tls.crt" -days 365 \
    -subj "/CN=xmc-dev-signer" -addext "subjectAltName = DNS:localhost, DNS:dex, IP:127.0.0.1" 2>/dev/null
  local sec
  for sec in gcp-sa-key backend-oauth-secrets validation-secret redis-secret; do
    kubectl delete secret "${sec}" -n "${NAMESPACE}" --ignore-not-found >/dev/null
  done
  kubectl create secret generic gcp-sa-key -n "${NAMESPACE}" --from-file="key.json=${WORK_DIR}/key.json"
  # The XID deployment mounts only backend-oauth-secrets, so key.json lives there too (as in test/e2e/lib/kind.py).
  kubectl create secret generic backend-oauth-secrets -n "${NAMESPACE}" \
    --from-literal="jwt-secret=e2e-development-secret-must-be-at-least-32-chars-long" \
    --from-literal="client-secret=xmc-client-secret" \
    --from-file="key.json=${WORK_DIR}/key.json"
  kubectl create secret generic validation-secret -n "${NAMESPACE}" \
    --from-file="tls.key=${WORK_DIR}/tls.key" --from-file="tls.crt=${WORK_DIR}/tls.crt"
  kubectl create secret generic redis-secret -n "${NAMESPACE}" --from-literal="redis-password="
  for sec in gcp-sa-key backend-oauth-secrets validation-secret redis-secret; do
    kubectl annotate secret "${sec}" -n "${NAMESPACE}" meta.helm.sh/release-name=xmc meta.helm.sh/release-namespace="${NAMESPACE}" --overwrite >/dev/null
    kubectl label secret "${sec}" -n "${NAMESPACE}" app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
  done
  rm -f "${WORK_DIR}/key.json" "${WORK_DIR}/tls.key" "${WORK_DIR}/tls.crt"
}

# Prints the public URL recorded on the gateway Service by "expose", or nothing before that.
discover_public_url() {
  local label
  label="$(kubectl get svc gateway-proxy-service -n "${NAMESPACE}" \
    -o jsonpath='{.metadata.annotations.service\.beta\.kubernetes\.io/azure-dns-label-name}' 2>/dev/null || true)"
  [[ -n "${label}" ]] && echo "http://${label}.${AZURE_LOCATION}.cloudapp.azure.com"
  return 0
}

write_values() {
  PUBLIC_URL="${PUBLIC_URL:-$(discover_public_url)}"
  if [[ -n "${PUBLIC_URL}" ]]; then
    FRONTEND_URL="${PUBLIC_URL%/}"
    DEX_ISSUER="${FRONTEND_URL}/dex"
  else
    FRONTEND_URL="http://localhost:8080"
    DEX_ISSUER="http://localhost:5556/dex"
  fi
  local pg_password
  pg_password="$(kubectl get secret xmc-postgres-credentials -n "${NAMESPACE}" -o jsonpath='{.data.password}' | base64 -d)"
  cat > "${WORK_DIR}/values-aks.yaml" <<EOF
projectId: "xmc-island"
environment: "azure"
clusterName: "${CLUSTER_NAME}"
clusterLocation: "${AZURE_LOCATION}"
isDevEnvironment: true
googleApplicationCredentials: "/etc/gcp-secrets/key.json"
# The services' own identity. CMS lets only this principal mutate component state, and xmc-bridge
# (like the actuator) authenticates as the Dex user below, so it must be that user, as in the e2e
# values (test/e2e/lib/helm_deployer.py).
gcpServiceAccount: "admin@xmc.local"
redisAddr: "redis-service.${NAMESPACE}.svc.cluster.local:6379"

ess_custom_image: "${ACR_LOGIN_SERVER}/xmc/experiment-state-server:latest"
actuator_custom_image: "${ACR_LOGIN_SERVER}/xmc/actuator:latest"
reconciler_custom_image: "${ACR_LOGIN_SERVER}/xmc/reconciler:latest"
xid_custom_image: "${ACR_LOGIN_SERVER}/xmc/xid-service:latest"
dashboard_service_custom_image: "${ACR_LOGIN_SERVER}/xmc/dashboard-service:latest"
health_service_custom_image: "${ACR_LOGIN_SERVER}/xmc/health-service:latest"
xmc_bridge_custom_image: "${ACR_LOGIN_SERVER}/xmc/xmc-bridge:latest"
ui:
  enabled: true
  image: "${ACR_LOGIN_SERVER}/xmc/ui:latest"
  apiUrl: "${FRONTEND_URL}/v1"

essDeployment:
  enabled: true
  hpa:
    minReplicas: 1
    maxReplicas: 2
xidService:
  hpa:
    minReplicas: 1
    maxReplicas: 2
healthServiceDeployment:
  enabled: true
dashboardServiceDeployment:
  enabled: true
# xmc-bridge reconciles Component CRs through CMS, which keeps components in memory when ESS has no
# Spanner client; the e2e suite's last stage checks that path.
componentsDeployment:
  enabled: true
  probeInterval: "2s"
  probeTimeout: "2s"
rodDeployment:
  enabled: false
logMonitorDeployment:
  enabled: false
ingestionGatewayDeployment:
  enabled: false
metrics:
  enabled: false
gateway:
  enabled: false

# Dex mints tokens with its configured issuer (http://localhost:5556/dex through port-forwards, or
# http://<dns label>.<location>.cloudapp.azure.com/dex once exposed); the services validate that
# issuer and fetch keys in-cluster.
auth:
  allowedEmails: "admin@xmc.local,viewer@xmc.local"
  clientId: "xmc-client"
  iap:
    clientId: "xmc-client"
    iapIssuer: "${DEX_ISSUER}"
  issuerUrl: "${DEX_ISSUER}"
  jwksUrl: "http://dex.dex.svc.cluster.local:5556/dex/keys"
  frontendUrl: "${FRONTEND_URL}"
  redirectUrl: "${FRONTEND_URL}/api/callback"
  authUrl: "${DEX_ISSUER}/auth"
  tokenUrl: "http://dex.dex.svc.cluster.local:5556/dex/token"
  apiUrl: "http://dex.dex.svc.cluster.local:5556/dex/userinfo"
  secureCookie: false
  username: "admin@xmc.local"
  password: "password123"

database:
  backend: "postgres"
  experimentalPostgres: true
  postgres:
    host: "postgres.${NAMESPACE}.svc.cluster.local"
    port: 5432
    user: "xmc"
    password: "${pg_password}"
    essDatabase: "ess"
    xidDatabase: "xid"
    sslMode: "disable"
EOF
}

sanitize_chart() {
  # Strip GKE-only resources but keep the Kueue queues (real Kueue is installed) and the HPAs
  # (AKS runs metrics-server), and keep imagePullPolicy: Always so :latest re-pulls on redeploy.
  (cd "${REPO_ROOT}" && python3 - "${WORK_DIR}/helm" <<'PYEOF'
import sys
from pathlib import Path
from tools.dev_env import chart_sanitizer as cs

cs.UNSUPPORTED_LOCAL_API_GROUPS = (
    "cloud.google.com", "networking.gke.io", "secrets-store.csi.x-k8s.io", "cert-manager.io",
    "acme.cert-manager.io", "gateway.networking.k8s.io", "autoscaling.k8s.io", "monitoring.googleapis.com",
)
cs.UNSUPPORTED_LOCAL_KINDS = (
    "BackendConfig", "ManagedCertificate", "HealthCheckPolicy", "SecretProviderClass",
    "VerticalPodAutoscaler", "Gateway", "HTTPRoute", "Issuer", "ClusterIssuer", "Certificate", "PodMonitoring",
)
cs.ChartSanitizer.patch_image_pull_policy = lambda self, chart_dir: 0
cs.ChartSanitizer(Path("deploy/helm"), Path(sys.argv[1])).prepare_sanitized_chart()
PYEOF
  )
}

# Writes a Helm post-renderer that adds the registry pull secret to every pod template.
write_pull_secret_post_renderer() {
  cat > "${WORK_DIR}/add-pull-secret.py" <<PYEOF
#!/usr/bin/env python3
import sys

import yaml

SECRET = {"name": "${PULL_SECRET}"}
TEMPLATED = ("Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job")


def pod_spec(doc):
    kind, spec = doc.get("kind"), doc.get("spec") or {}
    if kind in TEMPLATED:
        return spec.get("template", {}).get("spec")
    if kind == "CronJob":
        return spec.get("jobTemplate", {}).get("spec", {}).get("template", {}).get("spec")
    if kind == "Pod":
        return spec
    return None


docs = [d for d in yaml.safe_load_all(sys.stdin) if d]
for doc in docs:
    spec = pod_spec(doc)
    if spec is not None and SECRET not in spec.setdefault("imagePullSecrets", []):
        spec["imagePullSecrets"].append(SECRET)
yaml.safe_dump_all(docs, sys.stdout, default_flow_style=False, sort_keys=False)
PYEOF
  chmod +x "${WORK_DIR}/add-pull-secret.py"
}

step_xmc() {
  log "xmc: Helm release with database.backend=postgres"
  write_values
  sanitize_chart
  local post_render=()
  if kubectl get secret "${PULL_SECRET}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    write_pull_secret_post_renderer
    post_render=(--post-renderer "${WORK_DIR}/add-pull-secret.py")
  fi
  helm upgrade --install xmc "${WORK_DIR}/helm" -n "${NAMESPACE}" --create-namespace \
    -f "${REPO_ROOT}/deploy/helm/values-local.yaml" -f "${WORK_DIR}/values-aks.yaml" \
    "${post_render[@]}" --wait --timeout 8m || { kubectl get pods -n "${NAMESPACE}"; exit 1; }
  # ConfigMap-only changes do not roll pods; make sure every service runs with the current config.
  kubectl rollout restart deployment -n "${NAMESPACE}" -l 'app in (actuator,reconciler,xid-service,experiment-state-server,dashboard-service,health-service,xmc-bridge)' 2>/dev/null || true
  kubectl rollout status deployment/actuator-deployment -n "${NAMESPACE}" --timeout=180s
}

step_expose() {
  local cidrs="${ALLOWED_CIDRS:-$(curl -4 -s --max-time 10 ifconfig.me)/32}"
  local label="${DNS_LABEL:-${CLUSTER_NAME}}"
  log "expose: Azure load balancer (DNS label ${label}) in front of the gateway proxy, allowed from ${cidrs}"
  local dns_ip ranges="" c
  dns_ip="$(kubectl get svc kube-dns -n kube-system -o jsonpath='{.spec.clusterIP}')"
  for c in ${cidrs//,/ }; do ranges+="  - ${c}"$'\n'; done
  # gateway-nlb.yaml carries AWS NLB annotations; swap them for a DNS label and public IP tags.
  python3 - "${SCRIPT_DIR}/manifests/gateway-nlb.yaml" "${dns_ip}" "${ranges}" "${OWNER_TAG}" "${label}" <<'PYEOF' | kubectl apply -n "${NAMESPACE}" -f -
import re
import sys
src, dns_ip, ranges, owner, label = sys.argv[1:6]
text = open(src).read().replace("__CLUSTER_DNS__", dns_ip).replace("__OWNER_TAG__", owner)
text = text.replace("__SOURCE_RANGES__\n", ranges)
azure = (
    f'    service.beta.kubernetes.io/azure-dns-label-name: "{label}"\n'
    f'    service.beta.kubernetes.io/azure-pip-tags: "user={owner},owner={owner},purpose=xmc-postgres-island"\n'
)
text, n = re.subn(r"(?:    service\.beta\.kubernetes\.io/aws-[^\n]*\n)+", azure, text)
if n != 1:
    sys.exit("gateway-nlb.yaml: expected one block of AWS load balancer annotations")
print(text)
PYEOF
  kubectl rollout status deployment/gateway-proxy -n "${NAMESPACE}" --timeout=120s
  local ip=""
  for _ in $(seq 1 30); do
    ip="$(kubectl get svc gateway-proxy-service -n "${NAMESPACE}" -o jsonpath='{.status.loadBalancer.ingress[0].ip}')"
    [[ -n "${ip}" ]] && break
    sleep 10
  done
  [[ -z "${ip}" ]] && { echo "load balancer IP not assigned" >&2; exit 1; }
  local host="${label}.${AZURE_LOCATION}.cloudapp.azure.com"
  for _ in $(seq 1 30); do
    getent hosts "${host}" >/dev/null && break
    sleep 10
  done
  export PUBLIC_URL="http://${host}"
  echo "public URL: ${PUBLIC_URL} (${ip})"

  # Dex must mint tokens for the public issuer and accept the public callback.
  kubectl get configmap dex-config -n dex -o json | python3 -c '
import json, re, sys
url = sys.argv[1]
cm = json.load(sys.stdin)
cfg = re.sub(r"issuer: \S+", "issuer: " + url + "/dex", cm["data"]["config.yaml"], count=1)
cb = url + "/api/callback"
if cb not in cfg:
    cfg = cfg.replace("    redirectURIs:\n", "    redirectURIs:\n      - \x27" + cb + "\x27\n", 1)
cm["data"]["config.yaml"] = cfg
for k in ("resourceVersion", "uid", "creationTimestamp", "managedFields"):
    cm["metadata"].pop(k, None)
print(json.dumps(cm))' "${PUBLIC_URL}" | kubectl apply -f -
  kubectl rollout restart deployment/dex -n dex
  kubectl rollout status deployment/dex -n dex --timeout=120s

  step_xmc
  cat <<EOF

Public entry point (plain HTTP, allowed from ${cidrs}):
  UI + REST:  ${PUBLIC_URL}          (admin@xmc.local / password123)
  ESS gRPC:   ${host}:50051           (XMANAGER_ENDPOINT=http://${host}:50051 + XMC_AUTH_TOKEN from ${PUBLIC_URL}/dex/token)
Add a client later with:
  kubectl patch svc gateway-proxy-service -n ${NAMESPACE} --type=json -p '[{"op":"add","path":"/spec/loadBalancerSourceRanges/-","value":"<ip>/32"}]'
EOF
}

step_e2e() {
  PUBLIC_URL="${PUBLIC_URL:-$(discover_public_url)}"
  [[ -z "${PUBLIC_URL}" ]] && { echo "e2e needs the public URL; run the expose step first" >&2; exit 1; }
  local venv="${E2E_VENV:-${HOME}/.cache/xmc-e2e-venv}"
  local out="${E2E_SCREENSHOTS_DIR:-${WORK_DIR}/e2e}"
  log "e2e: Playwright suite against ${PUBLIC_URL} (screenshots and logs in ${out})"
  # The preemption scenario's queue uses the chart's cpu-flavor (test/e2e/lib/helm_deployer.py).
  kubectl apply -f "${REPO_ROOT}/test/e2e/manifests/preemption_queue.yaml"
  if [[ ! -x "${venv}/bin/python" ]]; then
    python3 -m venv "${venv}"
    "${venv}/bin/pip" install -q playwright requests
  fi
  "${venv}/bin/python" -m playwright install chromium >/dev/null
  mkdir -p "${out}"
  # Dex shares the UI host behind the gateway, so its URL must carry the /dex path.
  (cd "${REPO_ROOT}" && \
    XMC_BASE_URL="${PUBLIC_URL}" XMC_DEX_URL="${PUBLIC_URL}/dex" \
    KUBE_CONTEXT="$(kubectl config current-context)" XMC_SCREENSHOTS_DIR="${out}" \
    PYTHONPATH="${REPO_ROOT}" PYTHONUNBUFFERED=1 \
    "${venv}/bin/python" test/e2e/test_e2e_flow.py)
}

step_teardown() {
  log "teardown: deleting resource group ${RESOURCE_GROUP} (cluster, registry, node resource group)"
  az group delete -n "${RESOURCE_GROUP}" --yes --no-wait
  rm -f "${KUBECONFIG_FILE}"
  echo "deletion started; check with: az group show -n ${RESOURCE_GROUP}"
}

step_verify() {
  log "verify"
  kubectl get pods -n "${NAMESPACE}"
  kubectl get clusterqueue,localqueue -A
  PUBLIC_URL="${PUBLIC_URL:-$(discover_public_url)}"
  if [[ -n "${PUBLIC_URL}" ]]; then
    # Once exposed, Dex and the chart use the public URL, so localhost port-forwards cannot sign in.
    cat <<EOF

XMC is running on AKS cluster ${CLUSTER_NAME} (${AZURE_LOCATION}) with PostgreSQL, published at:
  UI + REST:  ${PUBLIC_URL}   (admin@xmc.local / password123)
  ESS gRPC:   ${PUBLIC_URL#http://}:50051
kubectl: KUBECONFIG=${KUBECONFIG_FILE}, or 'az aks get-credentials -g ${RESOURCE_GROUP} -n ${CLUSTER_NAME}' elsewhere.
EOF
    return
  fi
  cat <<EOF

XMC is running on AKS cluster ${CLUSTER_NAME} (${AZURE_LOCATION}) with PostgreSQL.
From your machine (after 'az aks get-credentials -g ${RESOURCE_GROUP} -n ${CLUSTER_NAME}', or with
KUBECONFIG=${KUBECONFIG_FILE} on this one):
  kubectl port-forward -n ${NAMESPACE} svc/gateway-proxy-service 8080:8080 &
  kubectl port-forward -n dex svc/dex 5556:5556 &
  kubectl port-forward -n ${NAMESPACE} svc/grpc-service 50051:50051 &
  open http://localhost:8080            # sign in as admin@xmc.local / password123
  export XMANAGER_ENDPOINT=http://127.0.0.1:50051 XMC_DOMAIN=localhost:8080 XMANAGER_UI_URL=http://localhost:8080
  xmanager login && python3 examples/simple_job/launch.py --queue=cpu-queue --num_work_units=1 --duration=30
EOF
}

main() {
  require_azure_login
  local steps=("$@")
  [[ ${#steps[@]} -eq 0 ]] && steps=(cluster controllers images postgres support xmc verify)
  local step
  for step in "${steps[@]}"; do
    case "${step}" in
      cluster|controllers|images|postgres|support|xmc|verify|expose|e2e|teardown) "step_${step}" ;;
      *) echo "unknown step: ${step}" >&2; exit 2 ;;
    esac
  done
}

main "$@"
