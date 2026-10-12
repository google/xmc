// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package gcemetadata provides utilities for extracting cluster metadata from environment variables or the GCE metadata server.
package gcemetadata

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/compute/metadata"
	"xmanager_cloud/internal/logging"
)

// Metadata holds cluster metadata such as project ID, location, and cluster name.
type Metadata struct {
	ProjectID   string
	Location    string
	ClusterName string
}

// Get extracts cluster metadata. If environment variables are set (either XMC_* or fallback GCP_*/CLUSTER_*),
// they are preferred to support non-GCP/local environments. Otherwise, it queries the GCE metadata server.
func Get(ctx context.Context) (Metadata, error) {
	proj := os.Getenv("XMC_PROJECT_ID")
	if proj == "" {
		proj = os.Getenv("GCP_PROJECT_ID")
	}

	cluster := os.Getenv("XMC_CLUSTER_NAME")
	if cluster == "" {
		cluster = os.Getenv("CLUSTER_NAME")
	}

	loc := os.Getenv("XMC_CLUSTER_LOCATION")
	if loc == "" {
		loc = os.Getenv("CLUSTER_LOCATION")
	}

	if proj != "" && cluster != "" && loc != "" {
		logging.Info(ctx, "Using cluster metadata from environment variables", "project_id", proj, "location", loc, "cluster_name", cluster)
		return Metadata{
			ProjectID:   proj,
			Location:    loc,
			ClusterName: cluster,
		}, nil
	}

	logging.Debug(ctx, "Some cluster metadata missing from environment; fetching from GCE Metadata...")
	if proj == "" {
		var err error
		proj, err = metadata.ProjectIDWithContext(ctx)
		if err != nil {
			return Metadata{}, fmt.Errorf("failed to get Project ID from metadata: %w", err)
		}
	}
	if cluster == "" {
		var err error
		cluster, err = metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
		if err != nil {
			return Metadata{}, fmt.Errorf("failed to get cluster-name from metadata: %w", err)
		}
	}
	if loc == "" {
		var err error
		loc, err = metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
		if err != nil {
			return Metadata{}, fmt.Errorf("failed to get cluster-location from metadata: %w", err)
		}
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", proj, "location", loc, "cluster_name", cluster)

	return Metadata{
		ProjectID:   proj,
		Location:    loc,
		ClusterName: cluster,
	}, nil
}
