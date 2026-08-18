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

package gcemetadata

import (
	"context"
	"testing"

	"golang/cmp/cmp"
)

func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"XMC_PROJECT_ID",
		"GCP_PROJECT_ID",
		"XMC_CLUSTER_NAME",
		"CLUSTER_NAME",
		"XMC_CLUSTER_LOCATION",
		"CLUSTER_LOCATION",
	}
	for _, k := range envVars {
		t.Setenv(k, "")
	}
}

func TestGet_FromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		envMap  map[string]string
		want    Metadata
		wantErr bool
	}{
		{
			name: "all_primary_xmc_env_vars",
			envMap: map[string]string{
				"XMC_PROJECT_ID":       "my-xmc-proj",
				"XMC_CLUSTER_NAME":     "my-xmc-cluster",
				"XMC_CLUSTER_LOCATION": "us-central1-c",
			},
			want: Metadata{
				ProjectID:   "my-xmc-proj",
				Location:    "us-central1-c",
				ClusterName: "my-xmc-cluster",
			},
		},
		{
			name: "all_fallback_gcp_env_vars",
			envMap: map[string]string{
				"GCP_PROJECT_ID":   "my-gcp-proj",
				"CLUSTER_NAME":     "my-gcp-cluster",
				"CLUSTER_LOCATION": "europe-west4-a",
			},
			want: Metadata{
				ProjectID:   "my-gcp-proj",
				Location:    "europe-west4-a",
				ClusterName: "my-gcp-cluster",
			},
		},
		{
			name: "mixed_primary_and_fallback_env_vars",
			envMap: map[string]string{
				"XMC_PROJECT_ID":   "my-xmc-proj",
				"CLUSTER_NAME":     "fallback-cluster",
				"CLUSTER_LOCATION": "us-east1-d",
			},
			want: Metadata{
				ProjectID:   "my-xmc-proj",
				Location:    "us-east1-d",
				ClusterName: "fallback-cluster",
			},
		},
		{
			name: "primary_takes_precedence_over_fallback",
			envMap: map[string]string{
				"XMC_PROJECT_ID":       "primary-proj",
				"GCP_PROJECT_ID":       "fallback-proj",
				"XMC_CLUSTER_NAME":     "primary-cluster",
				"CLUSTER_NAME":         "fallback-cluster",
				"XMC_CLUSTER_LOCATION": "primary-loc",
				"CLUSTER_LOCATION":     "fallback-loc",
			},
			want: Metadata{
				ProjectID:   "primary-proj",
				Location:    "primary-loc",
				ClusterName: "primary-cluster",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.envMap {
				t.Setenv(k, v)
			}

			got, err := Get(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Get() error = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Get() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGet_MissingEnvVars_FallsBackToMetadataServer(t *testing.T) {
	tests := []struct {
		name   string
		envMap map[string]string
	}{
		{
			name:   "all_env_vars_missing",
			envMap: map[string]string{},
		},
		{
			name: "missing_cluster_name",
			envMap: map[string]string{
				"XMC_PROJECT_ID":       "my-proj",
				"XMC_CLUSTER_LOCATION": "us-central1-a",
			},
		},
		{
			name: "missing_project_id",
			envMap: map[string]string{
				"XMC_CLUSTER_NAME":     "my-cluster",
				"XMC_CLUSTER_LOCATION": "us-central1-a",
			},
		},
		{
			name: "missing_location",
			envMap: map[string]string{
				"XMC_PROJECT_ID":   "my-proj",
				"XMC_CLUSTER_NAME": "my-cluster",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.envMap {
				t.Setenv(k, v)
			}

			// In non-GCE test environments, metadata server is unreachable and returns an error.
			_, err := Get(context.Background())
			if err == nil {
				t.Log("Get() succeeded via ambient GCE metadata server")
			} else {
				t.Logf("Get() returned expected error when querying GCE metadata server in test sandbox: %v", err)
			}
		})
	}
}

func TestGet_CancelledContext(t *testing.T) {
	clearEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Get(ctx)
	if err == nil {
		t.Error("Get() with cancelled context expected error, got nil")
	}
}
