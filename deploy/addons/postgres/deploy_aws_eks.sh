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

# Provisions an EKS cluster and deploys XMC on it with the PostgreSQL backend and no GCP
# dependencies ("island mode"): in-cluster PostgreSQL 16, Redis, Dex OIDC, JobSet and Kueue, and
# the XMC chart with database.backend=postgres. Mirrors the KinD dev flow (tools/dev_env) and the
# e2e auth values (test/e2e/lib/helm_deployer.py), adapted for EKS.
#
# Usage:
#   AWS_PROFILE=<sso profile> deploy/addons/postgres/deploy_aws_eks.sh [step...]
#   steps (default: all, in order): cluster controllers images postgres support xmc verify
#   extra step: expose   publishes the gateway through an AWS NLB (plain HTTP, IP-allowlisted) and
#                        re-points Dex and the chart at the NLB hostname (see ALLOWED_CIDRS)
#
# Environment:
#   AWS_PROFILE / AWS_REGION   AWS credentials and region (default region: eu-north-1).
#   CLUSTER_NAME               EKS cluster name (default: xmc-<ldap/$USER>).
#   OWNER_TAG                  Value of the `user`/`owner` tags on every AWS resource (default: $USER).
#   EKS_VERSION                Kubernetes version (default: 1.34; must be in standard support).
#   NODE_TYPE / NODE_COUNT     Managed node group shape (default: m6i.xlarge x 3).
#   JOBSET_CHART_VERSION       JobSet OCI chart (default: 0.12.0, as deploy/ctk_v2).
#   KUEUE_CHART_VERSION        Kueue OCI chart (default: 0.18.2, as deploy/ctk_v2; the chart's
#                              kueue-dev queues need the v1beta2 API).
#   NAMESPACE                  XMC namespace (default: xmc).
#   ALLOWED_CIDRS              "expose" only: comma-separated client CIDRs allowed through the NLB
#                              (default: this machine's public IP /32).
#   PUBLIC_URL                 Set automatically by "expose" (http://<nlb hostname>); pass it to a
#                              later "xmc" run to keep the public auth URLs.
#
# Afterwards, from any machine with kubectl access to the cluster:
#   kubectl port-forward -n xmc svc/gateway-proxy-service 8080:8080 &   # UI + REST at http://localhost:8080
#   kubectl port-forward -n dex svc/dex 5556:5556 &                     # Dex (login: admin@xmc.local / password123)
#   kubectl port-forward -n xmc svc/grpc-service 50051:50051 &          # ESS gRPC for the xmanager client
#   export XMANAGER_ENDPOINT=http://127.0.0.1:50051 XMC_DOMAIN=localhost:8080 XMANAGER_UI_URL=http://localhost:8080
#   xmanager login   # or XMC_AUTH_TOKEN=<Dex id_token> for non-interactive use
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
export PATH="${HOME}/.local/bin:${PATH}"

AWS_REGION="${AWS_REGION:-eu-north-1}"
export AWS_REGION
OWNER_TAG="${OWNER_TAG:-${USER:-xmc}}"
CLUSTER_NAME="${CLUSTER_NAME:-xmc-${OWNER_TAG}}"
EKS_VERSION="${EKS_VERSION:-1.34}"
NODE_TYPE="${NODE_TYPE:-m6i.xlarge}"
NODE_COUNT="${NODE_COUNT:-3}"
JOBSET_CHART_VERSION="${JOBSET_CHART_VERSION:-0.12.0}"
KUEUE_CHART_VERSION="${KUEUE_CHART_VERSION:-0.18.2}"
NAMESPACE="${NAMESPACE:-xmc}"
WORK_DIR="${WORK_DIR:-$(mktemp -d)}"

log() { echo; echo "=== $* ==="; }

require_aws_login() {
  if ! aws sts get-caller-identity >/dev/null 2>&1; then
    echo "AWS session not active; starting SSO device login for profile ${AWS_PROFILE:-default}..."
    aws sso login --use-device-code --no-browser
  fi
  ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
  ECR_REGISTRY="${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
  echo "AWS account ${ACCOUNT_ID}, region ${AWS_REGION}, cluster ${CLUSTER_NAME}"
}

step_cluster() {
  log "cluster: EKS ${CLUSTER_NAME} (${EKS_VERSION}, ${NODE_COUNT}x ${NODE_TYPE})"
  if aws eks describe-cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1; then
    echo "cluster exists; refreshing kubeconfig"
  else
    # No IAM OIDC provider / IRSA: sandbox roles often deny iam:CreateOpenIDConnectProvider, so the
    # EBS CSI driver gets its permissions from the node role instead (withAddonPolicies.ebs).
    cat > "${WORK_DIR}/eks-cluster.yaml" <<EOF
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: ${CLUSTER_NAME}
  region: ${AWS_REGION}
  version: "${EKS_VERSION}"
  tags:
    user: ${OWNER_TAG}
    owner: ${OWNER_TAG}
    purpose: xmc-postgres-island
addons:
  - name: vpc-cni
  - name: coredns
  - name: kube-proxy
  - name: aws-ebs-csi-driver
managedNodeGroups:
  - name: cpu-pool
    instanceType: ${NODE_TYPE}
    desiredCapacity: ${NODE_COUNT}
    minSize: 1
    maxSize: $((NODE_COUNT + 2))
    volumeSize: 80
    volumeType: gp3
    iam:
      withAddonPolicies:
        ebs: true
    tags:
      user: ${OWNER_TAG}
      owner: ${OWNER_TAG}
EOF
    eksctl create cluster -f "${WORK_DIR}/eks-cluster.yaml"
  fi
  aws eks update-kubeconfig --name "${CLUSTER_NAME}" >/dev/null
  kubectl get nodes
  kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  # The Postgres PVC needs a default StorageClass; gp2 ships without the annotation.
  if ! kubectl get storageclass -o jsonpath='{range .items[*]}{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}{"\n"}{end}' | grep -q true; then
    kubectl annotate storageclass gp2 storageclass.kubernetes.io/is-default-class="true" --overwrite
  fi
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
  log "images: build with Bazel and push to ${ECR_REGISTRY}/xmc/*"
  aws ecr get-login-password | docker login --username AWS --password-stdin "${ECR_REGISTRY}"
  (cd "${REPO_ROOT}" && bazel build --config=linux_amd64 \
    //cmd/experiment_state_server:experiment_state_server_image_tarball \
    //cmd/actuator:actuator_image_tarball \
    //cmd/reconciler:reconciler_image_tarball \
    //cmd/xid_service:xid_service_image_tarball \
    //cmd/dashboard_service:dashboard_service_image_tarball \
    //cmd/health_service:health_service_image_tarball \
    //internal/storage/postgres/migrations:postgres_migrator_image_tarball \
    && XMC_UI_PLATFORM=linux/amd64 bazel build //cmd/ui:ui_image_tarball)
  local -A tarballs=(
    ["experiment-state-server"]="cmd/experiment_state_server/experiment_state_server_image_tarball.tar"
    ["actuator"]="cmd/actuator/actuator_image_tarball.tar"
    ["reconciler"]="cmd/reconciler/reconciler_image_tarball.tar"
    ["xid-service"]="cmd/xid_service/xid_service_image_tarball.tar"
    ["dashboard-service"]="cmd/dashboard_service/dashboard_service_image_tarball.tar"
    ["health-service"]="cmd/health_service/health_service_image_tarball.tar"
    ["postgres-migrator"]="internal/storage/postgres/migrations/postgres_migrator_image_tarball.tar"
    ["ui"]="cmd/ui/ui_image_tarball.tar"
  )
  local svc repo loaded
  for svc in "${!tarballs[@]}"; do
    repo="xmc/${svc}"
    aws ecr describe-repositories --repository-names "${repo}" >/dev/null 2>&1 || \
      aws ecr create-repository --repository-name "${repo}" \
        --tags Key=user,Value="${OWNER_TAG}" Key=owner,Value="${OWNER_TAG}" >/dev/null
    loaded="$(docker load -i "${REPO_ROOT}/bazel-bin/${tarballs[${svc}]}" | sed -n 's/^Loaded image: //p' | tail -1)"
    docker tag "${loaded}" "${ECR_REGISTRY}/${repo}:latest"
    docker push "${ECR_REGISTRY}/${repo}:latest" | tail -1
  done
}

step_postgres() {
  log "postgres: in-cluster PostgreSQL 16 + ESS/XID migrations"
  XMC_NAMESPACE="${NAMESPACE}" MIGRATOR_IMAGE="${ECR_REGISTRY}/xmc/postgres-migrator:latest" \
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

write_values() {
  if [[ -n "${PUBLIC_URL:-}" ]]; then
    FRONTEND_URL="${PUBLIC_URL%/}"
    DEX_ISSUER="${FRONTEND_URL}/dex"
  else
    FRONTEND_URL="http://localhost:8080"
    DEX_ISSUER="http://localhost:5556/dex"
  fi
  local pg_password
  pg_password="$(kubectl get secret xmc-postgres-credentials -n "${NAMESPACE}" -o jsonpath='{.data.password}' | base64 -d)"
  cat > "${WORK_DIR}/values-eks.yaml" <<EOF
projectId: "xmc-island"
environment: "aws"
clusterName: "${CLUSTER_NAME}"
clusterLocation: "${AWS_REGION}"
isDevEnvironment: true
googleApplicationCredentials: "/etc/gcp-secrets/key.json"
gcpServiceAccount: "xmc-dev-sa@xmc-island.iam.gserviceaccount.com"
redisAddr: "redis-service.${NAMESPACE}.svc.cluster.local:6379"

ess_custom_image: "${ECR_REGISTRY}/xmc/experiment-state-server:latest"
actuator_custom_image: "${ECR_REGISTRY}/xmc/actuator:latest"
reconciler_custom_image: "${ECR_REGISTRY}/xmc/reconciler:latest"
xid_custom_image: "${ECR_REGISTRY}/xmc/xid-service:latest"
dashboard_service_custom_image: "${ECR_REGISTRY}/xmc/dashboard-service:latest"
health_service_custom_image: "${ECR_REGISTRY}/xmc/health-service:latest"
ui:
  enabled: true
  image: "${ECR_REGISTRY}/xmc/ui:latest"
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
rodDeployment:
  enabled: false
logMonitorDeployment:
  enabled: false
componentsDeployment:
  enabled: false
ingestionGatewayDeployment:
  enabled: false
metrics:
  enabled: false
gateway:
  enabled: false

# Dex mints tokens with its configured issuer (http://localhost:5556/dex through port-forwards, or
# http://<nlb>/dex once exposed); the services validate that issuer and fetch keys in-cluster.
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
  # (metrics-server is an EKS addon), and keep imagePullPolicy: Always so :latest re-pulls on redeploy.
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

step_xmc() {
  log "xmc: Helm release with database.backend=postgres"
  write_values
  sanitize_chart
  helm upgrade --install xmc "${WORK_DIR}/helm" -n "${NAMESPACE}" --create-namespace \
    -f "${REPO_ROOT}/deploy/helm/values-local.yaml" -f "${WORK_DIR}/values-eks.yaml" \
    --wait --timeout 8m || { kubectl get pods -n "${NAMESPACE}"; exit 1; }
  # ConfigMap-only changes do not roll pods; make sure every service runs with the current config.
  kubectl rollout restart deployment -n "${NAMESPACE}" -l 'app in (actuator,reconciler,xid-service,experiment-state-server,dashboard-service,health-service)' 2>/dev/null || true
  kubectl rollout status deployment/actuator-deployment -n "${NAMESPACE}" --timeout=180s
}

step_expose() {
  local cidrs="${ALLOWED_CIDRS:-$(curl -4 -s --max-time 10 ifconfig.me)/32}"
  log "expose: NLB in front of the gateway proxy, allowed from ${cidrs}"
  local dns_ip ranges="" c
  dns_ip="$(kubectl get svc kube-dns -n kube-system -o jsonpath='{.spec.clusterIP}')"
  for c in ${cidrs//,/ }; do ranges+="  - ${c}"$'\n'; done
  python3 - "${SCRIPT_DIR}/manifests/gateway-nlb.yaml" "${dns_ip}" "${ranges}" "${OWNER_TAG}" <<'PYEOF' | kubectl apply -n "${NAMESPACE}" -f -
import sys
src, dns_ip, ranges, owner = sys.argv[1:5]
text = open(src).read().replace("__CLUSTER_DNS__", dns_ip).replace("__OWNER_TAG__", owner)
print(text.replace("__SOURCE_RANGES__\n", ranges))
PYEOF
  kubectl rollout status deployment/gateway-proxy -n "${NAMESPACE}" --timeout=120s
  local host="" i
  for i in $(seq 1 30); do
    host="$(kubectl get svc gateway-proxy-service -n "${NAMESPACE}" -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')"
    [[ -n "${host}" ]] && break
    sleep 10
  done
  [[ -z "${host}" ]] && { echo "NLB hostname not assigned" >&2; exit 1; }
  export PUBLIC_URL="http://${host}"
  echo "public URL: ${PUBLIC_URL}"

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

step_verify() {
  log "verify"
  kubectl get pods -n "${NAMESPACE}"
  kubectl get clusterqueue,localqueue -A
  cat <<EOF

XMC is running on EKS cluster ${CLUSTER_NAME} (${AWS_REGION}) with PostgreSQL.
From your machine (after 'aws eks update-kubeconfig --name ${CLUSTER_NAME} --region ${AWS_REGION}'):
  kubectl port-forward -n ${NAMESPACE} svc/gateway-proxy-service 8080:8080 &
  kubectl port-forward -n dex svc/dex 5556:5556 &
  kubectl port-forward -n ${NAMESPACE} svc/grpc-service 50051:50051 &
  open http://localhost:8080            # sign in as admin@xmc.local / password123
  export XMANAGER_ENDPOINT=http://127.0.0.1:50051 XMC_DOMAIN=localhost:8080 XMANAGER_UI_URL=http://localhost:8080
  xmanager login && python3 examples/simple_job/launch.py --queue=cpu-queue --num_work_units=1 --duration=30
EOF
}

main() {
  require_aws_login
  local steps=("$@")
  [[ ${#steps[@]} -eq 0 ]] && steps=(cluster controllers images postgres support xmc verify)
  local step
  for step in "${steps[@]}"; do
    case "${step}" in
      cluster|controllers|images|postgres|support|xmc|verify|expose) "step_${step}" ;;
      *) echo "unknown step: ${step}" >&2; exit 2 ;;
    esac
  done
}

main "$@"
