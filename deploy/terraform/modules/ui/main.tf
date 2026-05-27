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
# API Service Enablement for Cloud Run
# -----------------------------------------------------------------------------
resource "google_project_service" "cloud_run_api" {
  project = var.project_id
  service = "run.googleapis.com"
}

# -----------------------------------------------------------------------------
# IAM Service Account for UI
# -----------------------------------------------------------------------------
resource "google_service_account" "ui_gsa" {
  project      = google_project_service.cloud_run_api.project
  account_id   = "${var.ui_gsa_name}-${var.environment}"
  display_name = "XManager on Cloud - UI Service Account (${var.environment})"
}

# -----------------------------------------------------------------------------
# Cloud Run Service for UI
# -----------------------------------------------------------------------------
resource "google_cloud_run_v2_service" "ui_service" {
  name     = "xmc-ui-${var.environment}"
  project  = google_project_service.cloud_run_api.project
  location = var.ui_service_location
  ingress  = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  deletion_protection = !var.is_dev_environment

  default_uri_disabled = true
  invoker_iam_disabled = true

  template {
    service_account = google_service_account.ui_gsa.email
    session_affinity = true

    scaling {
      min_instance_count = 1
      max_instance_count = 20
    }

    # 2. Container Configuration
    containers {
      name  = "xmc-ui"
      image = var.ui_image_url

      env {
        name  = "XMC_API_URL"
        value = var.api_url
      }

      env {
        name  = "XMC_REFRESH_INTERVAL_MS"
        value = var.refresh_interval_ms
      }

      env {
        name  = "XMC_DEFAULT_TO_CURSOR_PAGINATION"
        value = var.enable_cursor_pagination
      }

      env {
        name  = "XMC_STATUS_MESSAGE_HISTORY_ENABLED"
        value = var.enable_status_message_history
      }

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle = true
        startup_cpu_boost = true
      }

      startup_probe {
        timeout_seconds   = 30
        period_seconds    = 30
        failure_threshold = 1
        tcp_socket {
          port = 8080
        }
      }
    }

    vpc_access {
      network_interfaces {
        network    = "default"
        subnetwork = "default"
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    max_instance_request_concurrency = 80
    timeout                          = "300s"
  }
}
