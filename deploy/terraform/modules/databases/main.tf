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
# API Service Enablement for Spanner
# -----------------------------------------------------------------------------
resource "google_project_service" "spanner_api" {
  project            = var.project_id
  service            = "spanner.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iam_api" {
  project            = var.project_id
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Spanner Instance
# -----------------------------------------------------------------------------
resource "google_spanner_instance" "main_instance" {
  project          = google_project_service.spanner_api.project
  name             = "${var.spanner_instance_name}-${var.environment}"
  config           = var.spanner_instance_config
  display_name     = "XMC Instance (${var.environment})"
  processing_units = var.spanner_processing_units
}

# -----------------------------------------------------------------------------
# Spanner Database & Schema
# -----------------------------------------------------------------------------
resource "google_spanner_database" "experiment_database" {
  project             = google_project_service.spanner_api.project
  instance            = google_spanner_instance.main_instance.name
  name                = "${var.spanner_experiment_database_name}-${var.environment}"
  deletion_protection = !var.is_dev_environment
}

resource "google_spanner_database" "xid_database" {
  project             = google_project_service.spanner_api.project
  instance            = google_spanner_instance.main_instance.name
  name                = "${var.spanner_xid_database_name}-${var.environment}"
  deletion_protection = !var.is_dev_environment
}

# -----------------------------------------------------------------------------
# Spanner IAM Members
# -----------------------------------------------------------------------------
# Grants read/write access to the database to the services.
# -----------------------------------------------------------------------------
resource "google_spanner_database_iam_member" "actuator_database_users" {
  project  = google_project_service.spanner_api.project
  instance = google_spanner_instance.main_instance.name
  database = google_spanner_database.experiment_database.name
  role     = "roles/spanner.databaseUser"
  member   = var.actuator_principal
}

resource "google_spanner_database_iam_member" "reconciler_database_users" {
  project  = google_project_service.spanner_api.project
  instance = google_spanner_instance.main_instance.name
  database = google_spanner_database.experiment_database.name
  role     = "roles/spanner.databaseUser"
  member   = var.reconciler_principal
}

resource "google_spanner_database_iam_member" "experiment_database_users" {
  project  = google_project_service.spanner_api.project
  instance = google_spanner_instance.main_instance.name
  database = google_spanner_database.experiment_database.name
  role     = "roles/spanner.databaseUser"
  member   = var.ess_principal
}

resource "google_spanner_database_iam_member" "log_monitor_database_users" {
  project  = google_project_service.spanner_api.project
  instance = google_spanner_instance.main_instance.name
  database = google_spanner_database.experiment_database.name
  role     = "roles/spanner.databaseUser"
  member   = var.log_monitor_principal
}

resource "google_spanner_database_iam_member" "xid_database_users" {
  project  = google_project_service.spanner_api.project
  instance = google_spanner_instance.main_instance.name
  database = google_spanner_database.xid_database.name
  role     = "roles/spanner.databaseUser"
  member   = var.xid_principal

}