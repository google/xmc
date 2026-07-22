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
# API Service Enablement
# -----------------------------------------------------------------------------
resource "google_project_service" "container_api" {
  project            = var.project_id
  service            = "container.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "compute_api" {
  project            = var.project_id
  service            = "compute.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "artifactregistry_api" {
  project            = var.project_id
  service            = "artifactregistry.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iam_api" {
  project            = var.project_id
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

# If the variable enable_managed_mldiagnostics is true, it enables the API. If false, it does nothing.
resource "google_project_service" "hypercomputecluster_api" {
  count              = var.enable_managed_mldiagnostics ? 1 : 0
  project            = var.project_id
  service            = "hypercomputecluster.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Data Sources
# -----------------------------------------------------------------------------
data "google_compute_zones" "available" {
  project = google_project_service.compute_api.project
  region  = var.region
}

# -----------------------------------------------------------------------------
# IAM Service Account for GKE Nodes
# -----------------------------------------------------------------------------
resource "google_service_account" "gke_nodes_gsa" {
  project      = google_project_service.iam_api.project
  account_id   = "${var.gke_nodes_sa_account_id}-${var.environment}"
  display_name = "XManager on Cloud - GKE Nodes (${var.environment})"
}

resource "google_project_iam_member" "gke_nodes_ar_reader" {
  project = google_project_service.iam_api.project
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${google_service_account.gke_nodes_gsa.email}"
}

resource "google_project_iam_member" "gke_nodes_default_node_service_account" {
  project = google_project_service.iam_api.project
  role    = "roles/container.defaultNodeServiceAccount"
  member  = "serviceAccount:${google_service_account.gke_nodes_gsa.email}"
}

resource "google_project_iam_member" "gke_nodes_gcs_writer" {
  count   = var.enable_managed_mldiagnostics ? 1 : 0
  project = google_project_service.iam_api.project
  role    = "roles/storage.objectCreator"
  member  = "serviceAccount:${google_service_account.gke_nodes_gsa.email}"
}

# -----------------------------------------------------------------------------
# GKE Cluster
# -----------------------------------------------------------------------------
# Master authorized networks (GCP-0061) are omitted here because CIDR ranges are
# configured per environment during deployment/Cluster Toolkit blueprint setup.
#trivy:ignore:GCP-0061
resource "google_container_cluster" "main_cluster" {
  project             = google_project_service.container_api.project
  name                = "${var.cluster_name}-${var.environment}"
  location            = var.region
  deletion_protection = !var.is_dev_environment

  # We can't create a cluster with no node pool defined, but we want to only use
  # separately managed node pools. So we create the smallest possible default
  # node pool and immediately delete it.
  remove_default_node_pool = true
  initial_node_count       = 1

  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  ip_allocation_policy {
    cluster_ipv4_cidr_block  = "/14"
    services_ipv4_cidr_block = "/20"
  }

  vertical_pod_autoscaling {
    enabled = true
  }

  node_config {
    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
    metadata = {
      disable-legacy-endpoints = "true"
    }
  }
}

resource "google_container_node_pool" "main_pool" {
  project        = google_project_service.container_api.project
  name           = "main-pool"
  cluster        = google_container_cluster.main_cluster.name
  location       = var.region
  node_locations = data.google_compute_zones.available.names

  node_count = 1

  autoscaling {
    min_node_count = 1
    max_node_count = var.max_node_count
  }

  node_config {
    machine_type = var.machine_type

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }

    metadata = {
      disable-legacy-endpoints = "true"
    }

    # Enable Workload Identity on nodes
    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    service_account = google_service_account.gke_nodes_gsa.email

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }
}

resource "google_container_node_pool" "t4_pool" {
  project        = google_project_service.container_api.project
  name           = "t4-pool"
  cluster        = google_container_cluster.main_cluster.name
  location       = var.region
  node_locations = data.google_compute_zones.available.names

  autoscaling {
    total_min_node_count = 0
    total_max_node_count = 32
  }

  node_config {
    machine_type = "n1-standard-32"

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
    metadata = {
      disable-legacy-endpoints = "true"
    }
    labels = {
      "accelerator" = "nvidia-tesla-t4"
    }
    taint {
      key    = "nvidia.com/gpu"
      value  = "present"
      effect = "NO_SCHEDULE"
    }

    guest_accelerator {
      type  = "nvidia-tesla-t4"
      count = 4
    }

    # Enable Workload Identity on nodes
    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    service_account = google_service_account.gke_nodes_gsa.email

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }
}
