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

locals {
  is_valid      = var.cluster_id != null && var.cluster_id != ""
  cluster_parts = local.is_valid ? split("/", var.cluster_id) : []
  project       = length(local.cluster_parts) > 1 ? local.cluster_parts[1] : ""
  location      = length(local.cluster_parts) > 3 ? local.cluster_parts[3] : ""
  cluster_name  = length(local.cluster_parts) > 5 ? local.cluster_parts[5] : ""

  endpoint = var.cluster_endpoint != null ? var.cluster_endpoint : join("", data.google_container_cluster.target[*].endpoint)
  token    = var.access_token != null ? var.access_token : join("", data.google_client_config.default[*].access_token)

  all_auth = flatten(data.google_container_cluster.target[*].master_auth)
  ca_cert  = var.cluster_ca_certificate != null ? var.cluster_ca_certificate : (length(local.all_auth) > 0 ? local.all_auth[0].cluster_ca_certificate : "")
}

data "google_client_config" "default" {
  count = (var.enabled && local.is_valid && var.access_token == null) ? 1 : 0
}

data "google_container_cluster" "target" {
  count    = (var.enabled && local.is_valid && var.cluster_endpoint == null) ? 1 : 0
  name     = local.cluster_name
  location = local.location
  project  = local.project
}

provider "helm" {
  kubernetes {
    host                   = local.is_valid && local.endpoint != "" ? "https://${local.endpoint}" : null
    token                  = local.is_valid && local.token != "" ? local.token : null
    cluster_ca_certificate = local.is_valid && local.ca_cert != "" ? base64decode(local.ca_cert) : null
  }
}

resource "helm_release" "mldiagnostics_operator" {
  count = var.enabled ? 1 : 0

  name             = var.release_name
  chart            = var.chart_name
  version          = var.chart_version
  namespace        = var.namespace
  create_namespace = var.create_namespace
  timeout          = var.timeout
  wait             = var.wait

  dynamic "set" {
    for_each = length(var.dependencies) > 0 ? [1] : []
    content {
      name  = "tf_dependency_anchor"
      value = join(",", var.dependencies)
    }
  }

  lifecycle {
    precondition {
      condition     = !var.enable_managed_mldiagnostics
      error_message = "deploy_mldiagnostics_operator and enable_managed_mldiagnostics cannot both be true: both manage mldiagnostics-connection-operator in namespace gke-mldiagnostics."
    }
  }
}
