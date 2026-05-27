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
# API Service Enablement for Compute Engine
# -----------------------------------------------------------------------------
# Ensures the Compute Engine API is enabled on the project.
# This is required for the static IP resource below.
# -----------------------------------------------------------------------------
resource "google_project_service" "compute_api" {
  project            = var.project_id
  service            = "compute.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Static IP for Actuator HTTP traffic
# -----------------------------------------------------------------------------
resource "google_compute_global_address" "actuator_static_ip" {
  name    = "xmc-actuator-static-ip-${var.environment}"
  project = google_project_service.compute_api.project
}

# -----------------------------------------------------------------------------
# Static IP for ESS HTTP traffic
# -----------------------------------------------------------------------------
resource "google_compute_global_address" "ess_http_static_ip" {
  name    = "xmc-ess-http-static-ip-${var.environment}"
  project = google_project_service.compute_api.project
}

# -----------------------------------------------------------------------------
# Static IP for ESS gRPC traffic
# -----------------------------------------------------------------------------
resource "google_compute_global_address" "ess_grpc_static_ip" {
  name    = "xmc-ess-grpc-static-ip-${var.environment}"
  project = google_project_service.compute_api.project
}

# -----------------------------------------------------------------------------
# Static IP for UI load balancer
# -----------------------------------------------------------------------------
resource "google_compute_global_address" "ui_static_ip" {
  name    = "xmc-ui-static-ip-${var.environment}"
  project = google_project_service.compute_api.project
}