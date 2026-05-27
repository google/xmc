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
# API Service Enablement for Identity-Aware Proxy
# -----------------------------------------------------------------------------
resource "google_project_service" "iap_api" {
  project = var.project_id
  service = "iap.googleapis.com"
}

# -----------------------------------------------------------------------------
# Network Endpoint Group (NEG)
# -----------------------------------------------------------------------------
locals {
  iap_enabled = var.iap_client_id != "" && var.iap_client_id != null && var.iap_client_secret != "" && var.iap_client_secret != null
}

resource "google_compute_region_network_endpoint_group" "cloud_run_neg" {
  name                  = "xmc-ui-neg-${var.environment}"
  network_endpoint_type = "SERVERLESS"
  region                = var.ui_service_location
  project               = var.project_id

  cloud_run {
    service = var.ui_service_name
  }
}

# -----------------------------------------------------------------------------
# Backend Service
# -----------------------------------------------------------------------------
resource "google_compute_backend_service" "lb_backend" {
  name                  = "xmc-ui-backend-${var.environment}"
  protocol              = "HTTPS"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  project               = google_project_service.iap_api.project

  backend {
    group = google_compute_region_network_endpoint_group.cloud_run_neg.id
  }

  dynamic "iap" {
    for_each = local.iap_enabled ? [1] : []
    content {
      enabled              = true
      oauth2_client_id     = var.iap_client_id
      oauth2_client_secret = var.iap_client_secret
    }
  }
}

# -----------------------------------------------------------------------------
# Managed SSL Certificate
# -----------------------------------------------------------------------------
resource "google_compute_managed_ssl_certificate" "lb_cert" {
  name    = "xmc-ui-cert-${var.environment}"
  project = var.project_id

  managed {
    domains = ["ui.${var.domain_name}"]
  }
}

# -----------------------------------------------------------------------------
# URL Maps
# -----------------------------------------------------------------------------
resource "google_compute_url_map" "lb_url_map" {
  name            = "xmc-ui-url-map-${var.environment}"
  project         = var.project_id
  default_service = google_compute_backend_service.lb_backend.id
}

resource "google_compute_url_map" "http_redirect" {
  name    = "xmc-ui-http-redirect-${var.environment}"
  project = var.project_id

  default_url_redirect {
    https_redirect         = true
    strip_query            = false
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
  }
}

# -----------------------------------------------------------------------------
# Target HTTPS Proxy
# -----------------------------------------------------------------------------
resource "google_compute_target_https_proxy" "lb_https_proxy" {
  name             = "xmc-ui-https-proxy-${var.environment}"
  project          = var.project_id
  url_map          = google_compute_url_map.lb_url_map.id
  ssl_certificates = [google_compute_managed_ssl_certificate.lb_cert.id]
}

resource "google_compute_target_http_proxy" "http_redirect" {
  name    = "xmc-ui-http-proxy-${var.environment}"
  project = var.project_id
  url_map = google_compute_url_map.http_redirect.id
}

# -----------------------------------------------------------------------------
# Global Forwarding Rule
# -----------------------------------------------------------------------------
resource "google_compute_global_forwarding_rule" "lb_forwarding_rule" {
  name                  = "xmc-ui-forwarding-rule-${var.environment}"
  project               = var.project_id
  target                = google_compute_target_https_proxy.lb_https_proxy.id
  port_range            = "443"
  ip_address            = google_compute_global_address.ui_static_ip.address
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

resource "google_compute_global_forwarding_rule" "http_redirect" {
  name       = "xmc-ui-http-forwarding-rule-${var.environment}"
  project    = var.project_id
  target     = google_compute_target_http_proxy.http_redirect.id
  port_range = "80"

  ip_address            = google_compute_global_address.ui_static_ip.address
  load_balancing_scheme = "EXTERNAL_MANAGED"
}

# -----------------------------------------------------------------------------
# IAP Access Policy
# -----------------------------------------------------------------------------
resource "google_iap_web_backend_service_iam_member" "ui_iap_access" {
  for_each            = toset(var.allowed_users)
  project             = google_project_service.iap_api.project
  web_backend_service = google_compute_backend_service.lb_backend.name
  role                = "roles/iap.httpsResourceAccessor"
  member              = each.key
}
