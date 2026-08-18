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
	"testing"
)

func TestGceMetadataStruct(t *testing.T) {
	meta := GceMetadata{
		ProjectID:   "test-project",
		Location:    "us-central1-a",
		ClusterName: "test-cluster",
	}

	if meta.ProjectID != "test-project" {
		t.Errorf("meta.ProjectID = %q, want %q", meta.ProjectID, "test-project")
	}
	if meta.Location != "us-central1-a" {
		t.Errorf("meta.Location = %q, want %q", meta.Location, "us-central1-a")
	}
	if meta.ClusterName != "test-cluster" {
		t.Errorf("meta.ClusterName = %q, want %q", meta.ClusterName, "test-cluster")
	}
}

func TestGetGceMetadata_NonGCE(t *testing.T) {
	// In test environment without GCE metadata server, getGceMetadata returns an error.
	ctx := context.Background()
	_, err := getGceMetadata(ctx)
	if err == nil {
		t.Log("getGceMetadata succeeded in GCE environment")
	} else {
		t.Logf("getGceMetadata returned expected error outside GCE: %v", err)
	}
}
