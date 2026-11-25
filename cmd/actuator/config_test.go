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

package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	ee "xmanager_cloud/internal/toollibs/execution_environments"
)

func TestValidateExecutionEnvironmentConfig(t *testing.T) {
	tests := []struct {
		name          string
		envName       string
		placementJSON string
		want          ee.PlacementConfig
		wantErr       string
	}{
		{
			name:    "default_gke_empty_placement",
			envName: "gke",
			want:    ee.PlacementConfig{},
		},
		{
			name:          "kubernetes_with_valid_placement",
			envName:       "kubernetes",
			placementJSON: `{"gpu":{"schedulerName":"gang-scheduler","supplementalGroups":[2000]}}`,
			want: ee.PlacementConfig{
				GPU: ee.AcceleratorPlacement{
					SchedulerName:      "gang-scheduler",
					SupplementalGroups: []int64{2000},
				},
			},
		},
		{
			name:    "unknown_environment_rejected",
			envName: "nonexistent",
			wantErr: "unknown -default_execution_environment",
		},
		{
			name:          "malformed_placement_json_rejected",
			envName:       "kubernetes",
			placementJSON: `{invalid`,
			wantErr:       "invalid kubernetes_placement_config",
		},
		{
			name:          "invalid_supplemental_group_rejected",
			envName:       "kubernetes",
			placementJSON: `{"gpu":{"supplementalGroups":[0]}}`,
			wantErr:       "supplemental group ID must be positive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateExecutionEnvironmentConfig(tc.envName, tc.placementJSON)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("validateExecutionEnvironmentConfig() error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateExecutionEnvironmentConfig() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("validateExecutionEnvironmentConfig() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLoadPopulatesRuntimeKubernetesPlacement(t *testing.T) {
	t.Setenv("port", "8080")
	t.Setenv("xmanager_service_account", "sa@project.iam.gserviceaccount.com")
	t.Setenv("redis_addr", "localhost:6379")
	t.Setenv("kubernetes_placement_config", `{"gpu":{"schedulerName":"gang-scheduler","exclusivePlacement":true}}`)
	registerConfigs()
	origEnv := defaultExecutionEnvironment
	defaultExecutionEnvironment = "kubernetes"
	t.Cleanup(func() {
		defaultExecutionEnvironment = origEnv
	})

	cfg := Load()
	if cfg.RuntimeConfig.DefaultExecutionEnvironment != "kubernetes" {
		t.Errorf("DefaultExecutionEnvironment = %q, want %q", cfg.RuntimeConfig.DefaultExecutionEnvironment, "kubernetes")
	}
	wantPlacement := ee.PlacementConfig{
		GPU: ee.AcceleratorPlacement{
			SchedulerName:      "gang-scheduler",
			ExclusivePlacement: true,
		},
	}
	if diff := cmp.Diff(wantPlacement, cfg.RuntimeConfig.KubernetesPlacement); diff != "" {
		t.Errorf("KubernetesPlacement diff (-want +got):\n%s", diff)
	}
}
