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

func TestGetGceMetadata(t *testing.T) {
	tests := []struct {
		name    string
		envMap  map[string]string
		want    GceMetadata
		wantErr bool
	}{
		{
			name: "primary_env_vars",
			envMap: map[string]string{
				"XMC_PROJECT_ID":       "test-project-1",
				"XMC_CLUSTER_NAME":     "test-cluster-1",
				"XMC_CLUSTER_LOCATION": "us-central1-a",
			},
			want: GceMetadata{
				ProjectID:   "test-project-1",
				Location:    "us-central1-a",
				ClusterName: "test-cluster-1",
			},
		},
		{
			name: "fallback_env_vars",
			envMap: map[string]string{
				"GCP_PROJECT_ID":   "fallback-proj",
				"CLUSTER_NAME":     "fallback-cluster",
				"CLUSTER_LOCATION": "us-east1-b",
			},
			want: GceMetadata{
				ProjectID:   "fallback-proj",
				Location:    "us-east1-b",
				ClusterName: "fallback-cluster",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"XMC_PROJECT_ID", "GCP_PROJECT_ID", "XMC_CLUSTER_NAME", "CLUSTER_NAME", "XMC_CLUSTER_LOCATION", "CLUSTER_LOCATION"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.envMap {
				t.Setenv(k, v)
			}
			got, err := getGceMetadata(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("getGceMetadata() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("getGceMetadata() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGetGceMetadata_NonGCE(t *testing.T) {
	for _, k := range []string{"XMC_PROJECT_ID", "GCP_PROJECT_ID", "XMC_CLUSTER_NAME", "CLUSTER_NAME", "XMC_CLUSTER_LOCATION", "CLUSTER_LOCATION"} {
		t.Setenv(k, "")
	}
	ctx := context.Background()
	_, err := getGceMetadata(ctx)
	if err == nil {
		t.Log("getGceMetadata succeeded in GCE environment")
	} else {
		t.Logf("getGceMetadata returned expected error outside GCE: %v", err)
	}
}
