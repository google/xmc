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

set -eo pipefail

echo "==> Cleaning up previous runs"
kind delete cluster --name xmc-local-e2e || true

echo "==> Creating local KinD cluster"
kind create cluster --name xmc-local-e2e

echo "==> Building Bazel images"
bazel build //cmd/...

echo "==> Loading Bazel images into KinD"
for tarball in bazel-bin/cmd/*/*_image_tarball.tar; do
  docker load -i $tarball
done

kind load docker-image actuator_image:latest \
  experiment_state_server_image:latest \
  reconciler_image:latest \
  ui_image:latest \
  xid_service_image:latest \
  --name xmc-local-e2e

echo "==> Deploying Prerequisites (Cert-Manager, JobSet, Kueue)"
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.4/cert-manager.yaml
kubectl apply --server-side -f https://github.com/kubernetes-sigs/jobset/releases/download/v0.5.2/manifests.yaml
helm upgrade --install kueue oci://registry.k8s.io/kueue/charts/kueue --namespace kueue-system --create-namespace --version=0.18.2 --wait

echo "==> Deploying Redis cache"
helm repo add bitnami https://charts.bitnami.com/bitnami
helm upgrade --install redis bitnami/redis --set architecture=standalone --set auth.enabled=false --wait

# The XMC Helm charts contain GKE-specific resources (BackendConfig, ManagedCertificate, etc).
# Since KinD is a vanilla Kubernetes cluster, it does not have these CRDs installed by default.
# We inject empty dummy schemas here so that Helm does not crash during deployment when it encounters them.

echo "==> Injecting Dummy GKE CRDs"
kubectl apply -f - <<CRDS
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: backendconfigs.cloud.google.com
spec:
  group: cloud.google.com
  versions: [{name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, x-kubernetes-preserve-unknown-fields: true}}}}}]
  scope: Namespaced
  names: {plural: backendconfigs, singular: backendconfig, kind: BackendConfig}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: managedcertificates.networking.gke.io
spec:
  group: networking.gke.io
  versions: [{name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, x-kubernetes-preserve-unknown-fields: true}}}}}]
  scope: Namespaced
  names: {plural: managedcertificates, singular: managedcertificate, kind: ManagedCertificate}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: healthcheckpolicies.networking.gke.io
spec:
  group: networking.gke.io
  versions: [{name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, x-kubernetes-preserve-unknown-fields: true}}}}}]
  scope: Namespaced
  names: {plural: healthcheckpolicies, singular: healthcheckpolicy, kind: HealthCheckPolicy}
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: verticalpodautoscalers.autoscaling.k8s.io
spec:
  group: autoscaling.k8s.io
  versions: [{name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, x-kubernetes-preserve-unknown-fields: true}}}}}]
  scope: Namespaced
  names: {plural: verticalpodautoscalers, singular: verticalpodautoscaler, kind: VerticalPodAutoscaler}
CRDS

echo "==> Preparing Helm Configuration"
HELM_DIR=$(mktemp -d)
cp -r deploy/helm/* "$HELM_DIR/"
find "$HELM_DIR/templates" -type f -name "*.yaml" -exec sed -i 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/g' {} +
if [ -f "$HELM_DIR/templates/gateway/certificate.yaml" ]; then
    sed -i 's/namespace: xmc//g' "$HELM_DIR/templates/gateway/certificate.yaml"
fi
if [ -f "$HELM_DIR/templates/ui/healthcheckpolicy.yaml" ]; then
    sed -i 's/namespace: xmc//g' "$HELM_DIR/templates/ui/healthcheckpolicy.yaml"
fi
# Fix Kueue API version if the local cluster has obsolete Kueue CRDs
find "$HELM_DIR/templates/kueue-dev" -type f -name "*.yaml" -exec sed -i 's/kueue.x-k8s.io\/v1beta2/kueue.x-k8s.io\/v1beta1/g' {} + || true

DB_ID="e2e-xmc-local"
echo "==> Refreshing Ephemeral Spanner DB ($DB_ID)"
gcloud spanner databases create xids-db-${DB_ID} --instance=xmc-vluk-test --project=xmc-test-project-3 || true
gcloud spanner databases create ess-db-${DB_ID} --instance=xmc-vluk-test --project=xmc-test-project-3 || true

bazel run //internal/storage/spanner/migrations:migrations -- -command=up -project=xmc-test-project-3 -instance=xmc-vluk-test -database=xids-db-${DB_ID} -migrations_dir=xid
bazel run //internal/storage/spanner/migrations:migrations -- -command=up -project=xmc-test-project-3 -instance=xmc-vluk-test -database=ess-db-${DB_ID} -migrations_dir=ess

echo "==> Creating Auth Mounts"
kubectl create secret generic xmc-gcp-creds --from-file=key.json="${GOOGLE_APPLICATION_CREDENTIALS}" -n default

echo "==> Installing Helm Chart"
helm upgrade --install xmc-test "$HELM_DIR" \
  --set projectId=xmc-test-project-3 \
  --set environment=e2e \
  --set auth.mockAuth=true \
    --set ui.apiUrl="http://localhost:8081/v1" \
    --set auth.allowedOrigins="http://localhost:8080" \
    --set auth.allowedEmails="mock-user@mycompany.com" \
  --set secrets.development.jwtSecret="12345678901234567890123456789012" \
  --set ui.image=ui_image:latest \
  --set actuator.image=actuator_image:latest \
  --set essDeployment.image=experiment_state_server_image:latest \
  --set reconciler.image=reconciler_image:latest \
  --set xid.image=xid_service_image:latest \
  --set experimentStateServer.spannerDatabaseName="projects/xmc-test-project-3/instances/xmc-vluk-test/databases/ess-db-${DB_ID}" \
  --set xid.spannerDatabaseName="projects/xmc-test-project-3/instances/xmc-vluk-test/databases/xids-db-${DB_ID}" \
  --set "redisAddr=redis-master.default.svc.cluster.local" \
  --wait

echo "==> Patching runtime environment explicitly"
for deploy in actuator-deployment experiment-state-server-deployment reconciler-deployment xid-service-deployment; do
  kubectl set env deploy/$deploy \
    GOOGLE_APPLICATION_CREDENTIALS=/auth/key.json \
    XMC_CLUSTER_NAME=xmc-local-e2e \
    XMC_CLUSTER_LOCATION=local \
    XMC_PROJECT_ID=xmc-test-project-3
    
  kubectl patch deploy $deploy -n default --type='json' -p='[
    {"op": "add", "path": "/spec/template/spec/volumes/-", "value": {"name": "gcp-creds", "secret": {"secretName": "xmc-gcp-creds"}}},
    {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts/-", "value": {"name": "gcp-creds", "mountPath": "/auth", "readOnly": true}}
  ]' || echo "Volume patch ignored or duplicate"
done

echo "==> Cleaning up"
rm -rf "$HELM_DIR"

echo "==> Checking Pod Status"
kubectl get pods

echo "==> Ready!"
