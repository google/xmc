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

package resourceaccumulator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestCanonicalizeResourceType(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "cpu", raw: "cpu", want: "cpu"},
		{name: "memory", raw: "memory", want: "memory"},
		{name: "generic gpu", raw: "gpu", want: "nvidia.com/gpu"},
		{name: "k8s generic gpu", raw: "nvidia.com/gpu", want: "nvidia.com/gpu"},
		{name: "generic tpu", raw: "tpu", want: "google.com/tpu"},
		{name: "symbolic gpu enum", raw: "h100", want: "nvidia.com/gpu: nvidia-h100-80gb"},
		{name: "gke gpu label", raw: "nvidia-tesla-h100", want: "nvidia.com/gpu: nvidia-tesla-h100"},
		{name: "already compound gpu", raw: "nvidia.com/gpu: nvidia-l4", want: "nvidia.com/gpu: nvidia-l4"},
		{name: "specific tpu model", raw: "tpu-v5-lite-podslice", want: "google.com/tpu: tpu-v5-lite-podslice"},
		{name: "already compound tpu", raw: "google.com/tpu: tpu-v5p-slice", want: "google.com/tpu: tpu-v5p-slice"},
		{name: "unknown resource passes through", raw: "example.com/fpga", want: "example.com/fpga"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalizeResourceType(tc.raw); got != tc.want {
				t.Errorf("canonicalizeResourceType(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestFormatCompoundAccelerator(t *testing.T) {
	tests := []struct {
		name, resource, model, want string
	}{
		{name: "empty model", resource: "nvidia.com/gpu", model: "", want: "nvidia.com/gpu"},
		{name: "model equals resource", resource: "nvidia.com/gpu", model: "nvidia.com/gpu", want: "nvidia.com/gpu"},
		{name: "already compound", resource: "nvidia.com/gpu", model: "nvidia.com/gpu: nvidia-l4", want: "nvidia.com/gpu: nvidia-l4"},
		{name: "plain model", resource: "google.com/tpu", model: "tpu-v5p-slice", want: "google.com/tpu: tpu-v5p-slice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatCompoundAccelerator(tc.resource, tc.model); got != tc.want {
				t.Errorf("FormatCompoundAccelerator(%q, %q) = %q, want %q", tc.resource, tc.model, got, tc.want)
			}
		})
	}
}

func TestExtractNormalizedExecutableResources(t *testing.T) {
	if got := ExtractNormalizedExecutableResources(nil); len(got) != 0 {
		t.Fatalf("ExtractNormalizedExecutableResources(nil) = %v, want empty", got)
	}

	exec := &essmsgpb.Executable{
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{
					{
						Replicas: 2,
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_CpuResources{
									CpuResources: &essmsgpb.CpuResources{
										CpuMillicores: 2500,
										MemoryBytes:   4 * BytesPerGiB,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{
										Type:  "nvidia-tesla-h100",
										Count: 4,
									},
								},
							},
						},
					},
					{
						Spec: &essmsgpb.ExecutableSpec{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_TpuResources{
									TpuResources: &essmsgpb.TpuResources{
										Type:     "tpu-v5-lite-podslice",
										Topology: "2x2",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	want := map[string]float64{
		"cpu":                                  5.0,
		"memory":                               8.0,
		"nvidia.com/gpu: nvidia-tesla-h100":    4.0,
		"google.com/tpu: tpu-v5-lite-podslice": 4.0,
	}
	if diff := cmp.Diff(want, ExtractNormalizedExecutableResources(exec)); diff != "" {
		t.Errorf("ExtractNormalizedExecutableResources() mismatch (-want +got):\n%s", diff)
	}
}

func TestNormalizeUnits(t *testing.T) {
	tests := []struct {
		resourceType string
		value        float64
		want         float64
	}{
		{resourceType: "memory", value: 2 * BytesPerGiB, want: 2},
		{resourceType: "ephemeral-storage", value: BytesPerGiB / 2, want: 0.5},
		{resourceType: "cpu", value: 2.5, want: 2.5},
		{resourceType: "nvidia.com/gpu: nvidia-l4", value: 4, want: 4},
	}
	for _, tc := range tests {
		if got := NormalizeUnits(tc.resourceType, tc.value); got != tc.want {
			t.Errorf("NormalizeUnits(%q, %v) = %v, want %v", tc.resourceType, tc.value, got, tc.want)
		}
	}
}
