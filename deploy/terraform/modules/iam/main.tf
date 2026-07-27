# Copyright 2025 Google LLC
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
# -----------------------------------------------------------------------------
# API Service Enablement for IAM
# -----------------------------------------------------------------------------
resource "google_project_service" "iam_api" {
  project            = var.project_id
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iam_credentials_api" {
  project            = var.project_id
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}
resource "google_project_service" "cloudresourcemanager_api" {
  project            = var.project_id
  service            = "cloudresourcemanager.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Service Identities
# -----------------------------------------------------------------------------
resource "google_project_service_identity" "iap_sa" {
  provider = google-beta
  project  = var.project_id
  service  = "iap.googleapis.com"
}

# -----------------------------------------------------------------------------
# Data for Workload Identity Bindings
# -----------------------------------------------------------------------------

locals {
  wi_principal_prefix     = "principal://iam.googleapis.com/projects/${data.google_project.cluster.number}/locations/global/workloadIdentityPools/${data.google_project.cluster.project_id}.svc.id.goog/subject/ns/${var.k8s_namespace}"
  actuator_wi_principal   = "${local.wi_principal_prefix}/sa/${var.actuator_ksa_name}"
  reconciler_wi_principal = "${local.wi_principal_prefix}/sa/${var.reconciler_ksa_name}"
  ess_wi_principal        = "${local.wi_principal_prefix}/sa/${var.ess_ksa_name}"
  xid_wi_principal        = "${local.wi_principal_prefix}/sa/${var.xid_ksa_name}"
}

data "google_project" "cluster" {
  project_id = google_project_service.iam_api.project
}

# -----------------------------------------------------------------------------
# IAM Binding for Workload Identity Users
# -----------------------------------------------------------------------------
resource "google_project_iam_member" "wi_member_actuator" {
  project = data.google_project.cluster.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = local.actuator_wi_principal
}

resource "google_project_iam_member" "wi_member_reconciler" {
  project = data.google_project.cluster.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = local.reconciler_wi_principal
}

resource "google_project_iam_member" "wi_member_ess" {
  project = data.google_project.cluster.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = local.ess_wi_principal
}

resource "google_project_iam_member" "wi_member_xid" {
  project = data.google_project.cluster.project_id
  role    = "roles/iam.workloadIdentityUser"
  member  = local.xid_wi_principal
}

# -----------------------------------------------------------------------------
# OIDC Service Account for ESS Redis Tasks
# -----------------------------------------------------------------------------
resource "google_service_account" "ess_gsa" {
  project      = var.project_id
  account_id   = "${var.ess_gsa_name}-${var.environment}"
  display_name = "XManager on Cloud - Redis Tasks OIDC Service Account (${var.environment})"
}

resource "google_service_account_iam_member" "grant_act_as_permission" {
  service_account_id = google_service_account.ess_gsa.name
  role               = "roles/iam.serviceAccountUser"
  member             = local.ess_wi_principal
}
