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
moved {
  from = google_project_service.run_api
  to   = google_project_service.run_api[0]
}

resource "google_project_service" "run_api" {
  count              = var.log_monitor_deployment_enabled ? 0 : 1
  project            = var.project_id
  service            = "run.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "pubsub_api" {
  project            = var.project_id
  service            = "pubsub.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "logging_api" {
  project            = var.project_id
  service            = "logging.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service_identity" "pubsub_sa" {
  provider = google-beta
  project  = var.project_id
  service  = "pubsub.googleapis.com"
}

# When log_monitor_deployment_enabled is true, the Log Monitor runs in GKE via the Helm chart
# (deploy/helm/templates/log-monitor) and this Cloud Run service is not created.
moved {
  from = google_cloud_run_v2_service.log_monitor
  to   = google_cloud_run_v2_service.log_monitor[0]
}

resource "google_cloud_run_v2_service" "log_monitor" {
  count               = var.log_monitor_deployment_enabled ? 0 : 1
  provider            = google-beta
  project             = google_project_service.run_api[0].project
  name                = "xmc-log-monitor-${var.environment}"
  location            = var.region
  deletion_protection = false

  ingress = "INGRESS_TRAFFIC_INTERNAL_ONLY"

  template {
    service_account = var.service_account_email

    scaling {
      # min instance is added so that the service is always active and can
      # listen to the pubsub topic.
      min_instance_count = 1
      max_instance_count = 1
    }

    containers {
      image = var.image_url

      resources {
        cpu_idle = false
      }

      ports {
        container_port = 8080
      }

      startup_probe {
        initial_delay_seconds = 0
        timeout_seconds       = 5
        period_seconds        = 10
        failure_threshold     = 3
        http_get {
          path = "/healthz"
        }
      }

      liveness_probe {
        initial_delay_seconds = 0
        timeout_seconds       = 5
        period_seconds        = 30
        failure_threshold     = 3
        http_get {
          path = "/healthz"
        }
      }
      env {
        name  = "PUBSUB_SUBSCRIPTION_ID"
        value = google_pubsub_subscription.log_monitor_subscription.name
      }
      env {
        name  = "GOOGLE_CLOUD_PROJECT"
        value = var.project_id
      }
      env {
        name  = "DB_BACKEND"
        value = "spanner"
      }
      env {
        name  = "SPANNER_ESS_DATABASE"
        value = "projects/${var.project_id}/instances/xmc-${var.environment}/databases/experiment-db-${var.environment}"
      }
      env {
        name  = "SPANNER_QUOTA_PROJECT_ID"
        value = var.project_id
      }
      args = [
        "-log_keyword=${var.logging_sink_custom_keyword}",
        "-max_concurrent_updates=${var.max_concurrent_updates}",
      ]
    }
  }

  depends_on = [google_project_service.run_api]
}

resource "google_pubsub_topic" "log_monitor_topic" {
  project = google_project_service.pubsub_api.project
  name    = "${var.pubsub_topic_name}-${var.environment}"
}

resource "google_pubsub_topic" "log_monitor_dlq" {
  project = google_project_service.pubsub_api.project
  name    = "${var.pubsub_topic_name}-dlq-${var.environment}"
}

resource "google_logging_project_sink" "log_monitor_sink" {
  project                = google_project_service.logging_api.project
  name                   = "${var.logging_sink_name}-${var.environment}"
  destination            = "pubsub.googleapis.com/${google_pubsub_topic.log_monitor_topic.id}"
  filter                 = "resource.type=\"k8s_container\" AND labels.\"k8s-pod/app_kubernetes_io/managed-by\"=\"xmc\" AND labels.\"k8s-pod/xmc_google_com/experiment-id\":* AND labels.\"k8s-pod/xmc_google_com/work-unit-id\":* AND labels.\"k8s-pod/xmc_google_com/execution-id\":* AND (SEARCH(\"Traceback\") OR textPayload =~ \"${var.logging_sink_custom_keyword}\" OR jsonPayload.message =~ \"${var.logging_sink_custom_keyword}\")"
  unique_writer_identity = true
}

resource "google_pubsub_topic_iam_member" "log_monitor_sink_publisher" {
  project = google_project_service.pubsub_api.project
  topic   = google_pubsub_topic.log_monitor_topic.name
  role    = "roles/pubsub.publisher"
  member  = google_logging_project_sink.log_monitor_sink.writer_identity
}

resource "google_pubsub_topic_iam_member" "log_monitor_dlq_publisher" {
  project = google_project_service.pubsub_api.project
  topic   = google_pubsub_topic.log_monitor_dlq.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${google_project_service_identity.pubsub_sa.email}"
}

resource "google_pubsub_subscription_iam_member" "log_monitor_dlq_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.log_monitor_subscription.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_project_service_identity.pubsub_sa.email}"
}

resource "google_pubsub_subscription" "log_monitor_dlq_subscription" {
  project                    = var.project_id
  name                       = "${var.pubsub_subscription_name}-dlq-${var.environment}"
  topic                      = google_pubsub_topic.log_monitor_dlq.name
  message_retention_duration = "604800s" # 7 days
}

resource "google_pubsub_subscription" "log_monitor_subscription" {
  project              = var.project_id
  name                 = "${var.pubsub_subscription_name}-${var.environment}"
  topic                = google_pubsub_topic.log_monitor_topic.name
  ack_deadline_seconds = 60

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.log_monitor_dlq.id
    max_delivery_attempts = 5
  }

  retry_policy {
    minimum_backoff = "60s"
    maximum_backoff = "600s"
  }
}

resource "google_pubsub_subscription_iam_member" "log_monitor_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.log_monitor_subscription.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${var.service_account_email}"
}
