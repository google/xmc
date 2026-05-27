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
# API Service Enablement for Service Usage and Monitoring
# -----------------------------------------------------------------------------
resource "google_project_service" "service_usage_api" {
  project            = var.project_id
  service            = "serviceusage.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "monitoring_api" {
  project            = var.project_id
  service            = "monitoring.googleapis.com"
  disable_on_destroy = false
}

# ----------------------------------------------------------------------------- 
# Grant Service Usage Consumer Role
# This role allows the service accounts to use enabled services on the project.
# -----------------------------------------------------------------------------
resource "google_project_iam_member" "service_usage_consumer_actuator" {
  project = google_project_service.iam_api.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "${local.actuator_wi_principal}"
}

resource "google_project_iam_member" "service_usage_consumer_reconciler" {
  project = google_project_service.iam_api.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "${local.reconciler_wi_principal}"
}

resource "google_project_iam_member" "service_usage_consumer_ess" {
  project = google_project_service.iam_api.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "${local.ess_wi_principal}"
}

resource "google_project_iam_member" "service_usage_consumer_xid" {
  project = google_project_service.iam_api.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "${local.xid_wi_principal}"
}

resource "google_project_iam_member" "service_usage_consumer_log_monitor" {
  project = google_project_service.iam_api.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = var.log_monitor_principal
}

# -----------------------------------------------------------------------------
# Grant Monitoring Metric Writer Role
# This role allows the service accounts to write metric data to Cloud Monitoring.
# -----------------------------------------------------------------------------
resource "google_project_iam_member" "monitoring_metric_writer_actuator" {
  project = google_project_service.iam_api.project
  role    = "roles/monitoring.metricWriter"
  member  = "${local.actuator_wi_principal}"
}

resource "google_project_iam_member" "monitoring_metric_writer_ess" {
  project = google_project_service.iam_api.project
  role    = "roles/monitoring.metricWriter"
  member  = "${local.ess_wi_principal}"
}

resource "google_project_iam_member" "monitoring_metric_writer_xid" {
  project = google_project_service.iam_api.project
  role    = "roles/monitoring.metricWriter"
  member  = "${local.xid_wi_principal}"
}

resource "google_project_iam_member" "monitoring_metric_writer_log_monitor" {
  project = google_project_service.iam_api.project
  role    = "roles/monitoring.metricWriter"
  member  = var.log_monitor_principal
}
