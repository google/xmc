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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestExtractRequestedResources(t *testing.T) {
	tests := []struct {
		name string
		wu   *essmsgpb.WorkUnit
		want []*spb.Resource
	}{
		{
			name: "nil workunit",
			wu:   nil,
			want: nil,
		},
		{
			name: "empty workunit",
			wu:   &essmsgpb.WorkUnit{},
			want: nil,
		},
		{
			name: "workunit DTO conversion and order preservation",
			wu: buildWorkUnitWithJobSet([]*essmsgpb.KubernetesJob{
				{
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{
								CpuResources: &essmsgpb.CpuResources{CpuMillicores: 4000, MemoryBytes: 8192},
							},
						},
					},
					Sidecars: []*essmsgpb.ExecutableSpec{
						{
							Resources: &essmsgpb.ResourceSet{
								Resources: &essmsgpb.ResourceSet_GpuResources{
									GpuResources: &essmsgpb.GpuResources{Type: "nvidia-tesla-h100", Count: 2},
								},
							},
						},
					},
				},
			}),
			want: []*spb.Resource{
				{ResourceType: "cpu", Count: 4.0},
				{ResourceType: "memory", Count: 8192.0},
				{ResourceType: "nvidia-tesla-h100", Count: 2.0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRequestedResources(tt.wu)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("extractRequestedResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildResourceDTOs(t *testing.T) {
	totals := map[string]float64{
		"cpu":               4.0,
		"memory":            8192.0,
		"nvidia-tesla-h100": 2.0,
	}
	order := []string{"cpu", "memory", "nvidia-tesla-h100"}

	got := buildResourceDTOs(totals, order)
	want := []*spb.Resource{
		{ResourceType: "cpu", Count: 4.0},
		{ResourceType: "memory", Count: 8192.0},
		{ResourceType: "nvidia-tesla-h100", Count: 2.0},
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("buildResourceDTOs() mismatch (-want +got):\n%s", diff)
	}
}

func buildWorkUnitWithJobSet(jobs []*essmsgpb.KubernetesJob) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			Type: &essmsgpb.Executable_Jobset{
				Jobset: &essmsgpb.JobSetExecutable{
					Jobs: jobs,
				},
			},
		},
	}
}
