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
# -----------------------------------------------------------------------------
resource "google_project_service" "dns_api" {
  project            = var.project_id
  service            = "dns.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Central DNS zone for all dev environments.
# -----------------------------------------------------------------------------
resource "google_dns_managed_zone" "xmc_central_managed_zone" {
  project     = google_project_service.dns_api.project
  name        = var.managed_zone_name
  dns_name    = "${var.domain_name}."
  description = "Central managed zone for XMC dev environments"
}
