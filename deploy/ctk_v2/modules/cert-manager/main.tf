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

resource "google_certificate_manager_certificate" "default" {
  name        = "xmc-cert-${var.environment}"
  description = "Google-managed certificate for XMC ${var.environment}"
  scope       = "DEFAULT"
  project     = var.project_id
  managed {
    domains = var.domains
  }
}

resource "google_certificate_manager_certificate_map" "default" {
  name        = "xmc-cert-map-${var.environment}"
  description = "Certificate map for XMC ${var.environment}"
  project     = var.project_id
}

resource "google_certificate_manager_certificate_map_entry" "default" {
  for_each     = toset(var.domains)
  name         = "xmc-${var.environment}-${substr(md5(each.value), 0, 20)}"
  description  = "Map entry for ${each.value}"
  project      = var.project_id
  map          = google_certificate_manager_certificate_map.default.name
  certificates = [google_certificate_manager_certificate.default.id]
  hostname     = each.value
}
