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

package executionenvironments

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

func TestParsePlacementConfig(t *testing.T) {
	tests := []struct {
		name    string
		rawJSON string
		want    PlacementConfig
		wantErr string
	}{
		{
			name:    "empty_string",
			rawJSON: "",
			want:    PlacementConfig{},
		},
		{
			name:    "whitespace_only",
			rawJSON: "   \n\t  ",
			want:    PlacementConfig{},
		},
		{
			name:    "empty_json_object",
			rawJSON: "{}",
			want:    PlacementConfig{},
		},
		{
			name: "populated_gpu_and_tpu_profiles",
			rawJSON: `{
				"gpu": {
					"schedulerName": "gang-scheduler",
					"supplementalGroups": [2000, 2001],
					"exclusivePlacement": true,
					"nodeSelector": {"accelerator.example.com/gpu-class": "{{ .AcceleratorType }}"}
				},
				"tpu": {
					"podLabels": {"accelerator.example.com/topology": "{{ .Topology }}"}
				}
			}`,
			want: PlacementConfig{
				GPU: AcceleratorPlacement{
					SchedulerName:      "gang-scheduler",
					SupplementalGroups: []int64{2000, 2001},
					ExclusivePlacement: true,
					NodeSelector:       map[string]string{"accelerator.example.com/gpu-class": "{{ .AcceleratorType }}"},
				},
				TPU: AcceleratorPlacement{
					PodLabels: map[string]string{"accelerator.example.com/topology": "{{ .Topology }}"},
				},
			},
		},
		{
			name:    "unknown_field_rejected",
			rawJSON: `{"gpu": {"schedularName": "typo"}}`,
			wantErr: "unknown field",
		},
		{
			name:    "malformed_json_rejected",
			rawJSON: `{"gpu": [}`,
			wantErr: "failed to parse placement config JSON",
		},
		{
			name:    "trailing_json_tokens_rejected",
			rawJSON: `{}{"extra": true}`,
			wantErr: "trailing data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePlacementConfig(tc.rawJSON)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParsePlacementConfig() error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePlacementConfig() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ParsePlacementConfig() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExclusiveResourceNames(t *testing.T) {
	tests := []struct {
		name string
		cfg  PlacementConfig
		want []string
	}{
		{
			name: "none_exclusive",
			cfg:  PlacementConfig{},
			want: nil,
		},
		{
			name: "gpu_only",
			cfg:  PlacementConfig{GPU: AcceleratorPlacement{ExclusivePlacement: true}},
			want: []string{constants.NvidiaGPUResourceName},
		},
		{
			name: "gpu_and_tpu",
			cfg: PlacementConfig{
				GPU: AcceleratorPlacement{ExclusivePlacement: true},
				TPU: AcceleratorPlacement{ExclusivePlacement: true},
			},
			want: []string{constants.NvidiaGPUResourceName, constants.TPUResourceName},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ExclusiveResourceNames()
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExclusiveResourceNames() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewKubernetesValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PlacementConfig
		wantErr string
	}{
		{
			name:    "zero_supplemental_group_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{SupplementalGroups: []int64{0}}},
			wantErr: "supplemental group ID must be positive",
		},
		{
			name:    "negative_supplemental_group_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{SupplementalGroups: []int64{-5}}},
			wantErr: "supplemental group ID must be positive",
		},
		{
			name:    "invalid_scheduler_name_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{SchedulerName: "Invalid_Scheduler!"}},
			wantErr: "invalid gpu.schedulerName",
		},
		{
			name:    "invalid_tpu_profile_also_validated",
			cfg:     PlacementConfig{TPU: AcceleratorPlacement{SchedulerName: "Invalid_Scheduler!"}},
			wantErr: "invalid tpu.schedulerName",
		},
		{
			name:    "invalid_label_key_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodLabels: map[string]string{"bad/key/extra": "v"}}},
			wantErr: "invalid key",
		},
		{
			name:    "reserved_xmc_label_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{JobSetLabels: map[string]string{constants.ExperimentIDLabel: "99"}}},
			wantErr: "reserved system metadata key",
		},
		{
			name:    "reserved_kueue_label_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodLabels: map[string]string{"kueue.x-k8s.io/queue-name": "other"}}},
			wantErr: "reserved system metadata key",
		},
		{
			name:    "reserved_managed_by_label_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{JobSetLabels: map[string]string{constants.ManagedByLabel: "custom"}}},
			wantErr: "reserved system metadata key",
		},
		{
			name:    "reserved_autoscaler_annotation_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{JobSetAnnotations: map[string]string{constants.AutoscalerSafeToEvictAnnotation: "true"}}},
			wantErr: "reserved system metadata key",
		},
		{
			name:    "reserved_xmc_dev_annotation_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodAnnotations: map[string]string{"xmc.dev/custom": "val"}}},
			wantErr: "reserved system metadata key",
		},
		{
			name:    "unclosed_template_syntax_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodLabels: map[string]string{"example.com/k": "{{ .AcceleratorType"}}},
			wantErr: "failed to parse template",
		},
		{
			name:    "unknown_template_field_rejected_at_compile",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodLabels: map[string]string{"example.com/k": "{{ .NonExistent }}"}}},
			wantErr: "can't evaluate field NonExistent",
		},
		{
			name:    "truncHash_too_short_rejected_at_compile",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{JobSetLabels: map[string]string{"example.com/group": "{{ truncHash 8 .JobSetName }}"}}},
			wantErr: "truncHash maxLen must be at least 10",
		},
		{
			name:    "multibyte_invalid_label_value_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{NodeSelector: map[string]string{"example.com/k": "λ-invalid"}}},
			wantErr: "not a valid Kubernetes label value",
		},
		{
			name:    "invalid_pod_annotation_key_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{PodAnnotations: map[string]string{"/leading-slash": "ok"}}},
			wantErr: "invalid key",
		},
		{
			name:    "invalid_node_selector_key_rejected",
			cfg:     PlacementConfig{GPU: AcceleratorPlacement{NodeSelector: map[string]string{"spaces not allowed": "v"}}},
			wantErr: "invalid key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewKubernetes(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NewKubernetes() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestTruncHash(t *testing.T) {
	tests := []struct {
		name    string
		maxLen  int
		input   string
		want    string
		wantErr bool
	}{
		{
			name:   "shorter_than_limit_unchanged",
			maxLen: 28,
			input:  "xmc-operation-100-200",
			want:   "xmc-operation-100-200",
		},
		{
			name:   "exact_limit_unchanged",
			maxLen: 21,
			input:  "xmc-operation-100-200",
			want:   "xmc-operation-100-200",
		},
		{
			name:    "below_minimum_length_returns_error",
			maxLen:  9,
			input:   "xmc-operation-100-200",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := truncHash(tc.maxLen, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("truncHash(%d, %q) expected error, got %q", tc.maxLen, tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("truncHash(%d, %q) unexpected error: %v", tc.maxLen, tc.input, err)
			}
			if got != tc.want {
				t.Errorf("truncHash(%d, %q) = %q, want %q", tc.maxLen, tc.input, got, tc.want)
			}
		})
	}

	t.Run("long_inputs_bounded_and_collision_free", func(t *testing.T) {
		const limit = 24
		first := "xmc-operation-experiment-alpha-0001"
		second := "xmc-operation-experiment-alpha-0002"

		gotFirst, err := truncHash(limit, first)
		if err != nil {
			t.Fatalf("truncHash() error: %v", err)
		}
		gotSecond, err := truncHash(limit, second)
		if err != nil {
			t.Fatalf("truncHash() error: %v", err)
		}
		if len(gotFirst) > limit {
			t.Errorf("len(%q) = %d, want <= %d", gotFirst, len(gotFirst), limit)
		}
		if gotFirst == gotSecond {
			t.Errorf("distinct inputs produced identical truncated value %q", gotFirst)
		}
		if strings.Contains(gotFirst, "--") {
			t.Errorf("truncHash(%q) = %q should trim trailing hyphens before hash suffix", first, gotFirst)
		}
	})

	t.Run("multibyte_utf8_input_preserves_valid_utf8_and_rune_bound", func(t *testing.T) {
		const limit = 14
		input := "αβγδεζηθικλμνξοπρσ"
		got, err := truncHash(limit, input)
		if err != nil {
			t.Fatalf("truncHash() error: %v", err)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncHash(%q) = %q produced invalid UTF-8", input, got)
		}
		if len([]rune(got)) > limit {
			t.Errorf("rune length of %q = %d, want <= %d", got, len([]rune(got)), limit)
		}
	})
}
