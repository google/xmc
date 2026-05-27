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
# API Service Enablement for DNS
# -----------------------------------------------------------------------------
# Ensures the DNS API is enabled on the project.
# This is required for the DNS records below.
# -----------------------------------------------------------------------------
resource "google_project_service" "dns_api" {
  project            = var.project_id
  service            = "dns.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# DNS zone for the domain name.
# -----------------------------------------------------------------------------
resource "google_dns_managed_zone" "xmc_domain_managed_zone" {
  project     = google_project_service.dns_api.project
  name        = "xmc-${var.environment}-zone"
  dns_name    = "${var.domain_name}."
  description = "Managed zone for XMC domain"
}

# -----------------------------------------------------------------------------
# A record for the API.
# -----------------------------------------------------------------------------
resource "google_dns_record_set" "xmc_api_a_record" {
  project      = google_project_service.dns_api.project
  managed_zone = google_dns_managed_zone.xmc_domain_managed_zone.name

  name    = "api.${var.domain_name}."
  type    = "A"
  ttl     = 300
  rrdatas = [google_compute_global_address.ess_http_static_ip.address]
}

# -----------------------------------------------------------------------------
# A record for the actuator API.
# -----------------------------------------------------------------------------
resource "google_dns_record_set" "xmc_actuator_a_record" {
  project      = google_project_service.dns_api.project
  managed_zone = google_dns_managed_zone.xmc_domain_managed_zone.name

  name    = "actuator.api.${var.domain_name}."
  type    = "A"
  ttl     = 300
  rrdatas = [google_compute_global_address.actuator_static_ip.address]
}

# -----------------------------------------------------------------------------
# A record for the gRPC API.
# -----------------------------------------------------------------------------
resource "google_dns_record_set" "xmc_grpc_a_record" {
  project      = google_project_service.dns_api.project
  managed_zone = google_dns_managed_zone.xmc_domain_managed_zone.name

  name    = "grpc.api.${var.domain_name}."
  type    = "A"
  ttl     = 300
  rrdatas = [google_compute_global_address.ess_grpc_static_ip.address]
}

# -----------------------------------------------------------------------------
# A record for the UI.
# -----------------------------------------------------------------------------
resource "google_dns_record_set" "xmc_ui_a_record" {
  project      = google_project_service.dns_api.project
  managed_zone = google_dns_managed_zone.xmc_domain_managed_zone.name

  name    = "ui.${var.domain_name}."
  type    = "A"
  ttl     = 300
  rrdatas = [google_compute_global_address.ui_static_ip.address]
}