// Copyright 2025 Google LLC
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
package main

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/compute/metadata/v/v0/metadata"
	"xmanager_cloud/logging/logging"
)

// GceMetadata holds cluster metadata fetched from GCE metadata server.
type GceMetadata struct {
	ProjectID   string
	Location    string
	ClusterName string
}

func getGceMetadata(ctx context.Context) (GceMetadata, error) {
	logging.Debug(ctx, "Fetching GCE Metadata...")
	projectID, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get Project ID from metadata: %w", err)
	}
	clusterName, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-name from metadata: %w", err)
	}
	location, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-location from metadata: %w", err)
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", projectID, "location", location, "cluster_name", clusterName)
	return GceMetadata{
		ProjectID:   projectID,
		Location:    location,
		ClusterName: clusterName,
	}, nil
}
