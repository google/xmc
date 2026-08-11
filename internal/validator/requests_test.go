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
package validator

import (
	"errors"
	"strings"
	"testing"
	"time"

	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/go-cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"task/go/status"
	essapipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmessagespb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestValidateCreateXidRequest(t *testing.T) {
	validReq := &xidapipb.CreateXidEntryRequest{
		XidEntry: &xidmessagespb.XidEntry{
			Acls: &xidmessagespb.XidAcls{
				Owners: []*xidmessagespb.XidAcls_Role{
					{Principal: &xidmessagespb.XidAcls_Role_Group{Group: "group"}},
				},
			},
			Origin: &xidmessagespb.OriginatingSystem{},
		},
	}

	tests := []struct {
		name    string
		req     *xidapipb.CreateXidEntryRequest
		wantErr error
	}{
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_xid_entry",
			req:     &xidapipb.CreateXidEntryRequest{},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "xid_entry_name_not_empty",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Name = proto.String("xids/123")
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_acls",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_acls",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls = &xidmessagespb.XidAcls{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_origin",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Origin = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_owner_role",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls.Owners = []*xidmessagespb.XidAcls_Role{{}}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_writer_role",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls.Writers = []*xidmessagespb.XidAcls_Role{{}}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_reader_role",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls.Readers = []*xidmessagespb.XidAcls_Role{{}}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "valid_request",
			req:     validReq,
			wantErr: nil,
		},
		{
			name: "valid_parent_xid",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/123")
			}),
			wantErr: nil,
		},
		{
			name: "valid_parent_xid_nil_acls",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/123")
				r.XidEntry.Acls = nil
			}),
			wantErr: nil,
		},
		{
			name: "invalid_parent_xid",
			req: proto.Modify(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("invalid/123")
			}),
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateXidRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateXidRequest(%v) returned unexpected error: %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateExperimentRequest(t *testing.T) {
	validReq := &essapipb.CreateExperimentRequest{
		Experiment: &essmsgpb.Experiment{
			Title: "title",
			Acls:  &xidmessagespb.XidAcls{},
		},
	}

	tests := []struct {
		name    string
		req     *essapipb.CreateExperimentRequest
		wantErr error
	}{
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "nil_experiment",
			req:     &essapipb.CreateExperimentRequest{},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Name = "experiments/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "creator_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Creator = "user"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Title = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Title = " "
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "status_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Status = &essmsgpb.ExperimentStatus{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Urls = make([]*essmsgpb.Url, maxURLs+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Urls = []*essmsgpb.Url{
					{
						Url: strings.Repeat("a", maxURLLen+1),
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_acls",
			req: proto.Modify(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "valid_request",
			req:     validReq,
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateExperimentRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateExperimentRequest(%v) returned unexpected error: %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestValidateUpdateExperimentRequest(t *testing.T) {
	validReq := &essapipb.UpdateExperimentRequest{
		Experiment: &essmsgpb.Experiment{
			Title: "title",
		},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	}

	tests := []struct {
		name    string
		req     *essapipb.UpdateExperimentRequest
		wantErr error
	}{
		{
			name: "title_empty",
			req: proto.Modify(validReq, func(r *essapipb.UpdateExperimentRequest) {
				r.Experiment.Title = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: proto.Modify(validReq, func(r *essapipb.UpdateExperimentRequest) {
				r.Experiment.Title = " "
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "valid_request",
			req:     validReq,
			wantErr: nil,
		},
		{
			name: "title_empty_no_mask",
			req: proto.Modify(validReq, func(r *essapipb.UpdateExperimentRequest) {
				r.Experiment.Title = ""
				r.UpdateMask = &fmpb.FieldMask{Paths: []string{}}
			}),
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateUpdateExperimentRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateUpdateExperimentRequest(%v) returned unexpected error: %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestFillExperimentNullValues(t *testing.T) {
	exp := &essmsgpb.Experiment{}
	got := FillExperimentNullValues(exp)
	want := &essmsgpb.Experiment{
		Urls:     []*essmsgpb.Url{},
		Tags:     []string{},
		Priority: essmsgpb.Priority_LOW,
	}
	if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
		t.Errorf("FillExperimentNullValues(%v) returned unexpected diff (-got +want):\n%s", exp, diff)
	}
}

func TestFillWorkUnitNullValues(t *testing.T) {
	wu := &essmsgpb.WorkUnit{}
	got := FillWorkUnitNullValues(wu)
	want := &essmsgpb.WorkUnit{
		Urls:     []*essmsgpb.Url{},
		Tags:     []string{},
		Priority: essmsgpb.Priority_LOW,
	}
	if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
		t.Errorf("FillWorkUnitNullValues(%v) returned unexpected diff (-got +want):\n%s", wu, diff)
	}
}

func TestFillArtifactNullValues(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		art := &essmsgpb.Artifact{}
		got := FillArtifactNullValues(art)
		want := &essmsgpb.Artifact{
			Tags: []string{},
			Url:  &essmsgpb.Url{},
		}
		if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
			t.Errorf("FillArtifactNullValues(%v) returned unexpected diff (-got +want):\n%s", art, diff)
		}
	})

	t.Run("managed_xprof_keeps_unspecified_phase", func(t *testing.T) {
		art := &essmsgpb.Artifact{
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "display",
						RunGroup:    "group",
					},
				},
			},
		}
		got := FillArtifactNullValues(art)
		want := &essmsgpb.Artifact{
			Tags: []string{},
			Url:  &essmsgpb.Url{},
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "display",
						RunGroup:    "group",
					},
				},
			},
		}
		if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
			t.Errorf("FillArtifactNullValues(%v) returned unexpected diff (-got +want):\n%s", art, diff)
		}
	})
}

func TestFillXidEntryNullValues(t *testing.T) {
	xid := &xidmessagespb.XidEntry{}
	got := FillXidEntryNullValues(xid)
	want := &xidmessagespb.XidEntry{
		Acls: &xidmessagespb.XidAcls{},
	}
	if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
		t.Errorf("FillXidEntryNullValues(%v) returned unexpected diff (-got +want):\n%s", xid, diff)
	}
}

func TestValidateCreateWorkUnitRequest(t *testing.T) {
	validReq := &essapipb.CreateWorkUnitRequest{
		Parent: "experiments/123",
		WorkUnit: &essmsgpb.WorkUnit{
			Executable: &essmsgpb.Executable{
				SchedulingConstraints: &essmsgpb.SchedulingConstraints{
					QueueName: "queue",
				},
				Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
					Jobs: []*essmsgpb.KubernetesJob{
						{
							Name:     "job1",
							Replicas: 1,
							Spec: &essmsgpb.ExecutableSpec{
								DockerImage: "image",
								Resources: &essmsgpb.ResourceSet{
									Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
										CpuMillicores: 1,
										MemoryBytes:   1,
									}},
								},
							},
						},
					},
				}},
			},
			WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		},
	}

	tests := []struct {
		name    string
		req     *essapipb.CreateWorkUnitRequest
		wantErr error
	}{
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.Parent = "invalid/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_work_unit",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Name = "experiments/123/workUnits/1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "current_work_unit_execution_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.CurrentWorkUnitExecution = &essmsgpb.WorkUnitExecution{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Urls = make([]*essmsgpb.Url, maxURLs+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Urls = []*essmsgpb.Url{
					{
						Url: strings.Repeat("a", maxURLLen+1),
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_executable",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "work_unit_type_unspecified",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.WorkUnitType = essmsgpb.WorkUnit_WORK_UNIT_TYPE_UNSPECIFIED
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_scheduling_constraints",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_queue_name",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints.QueueName = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "queue_name_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints.QueueName = strings.Repeat("a", maxQueueNameLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_jobset",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.Type = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "no_jobs",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = []*essmsgpb.KubernetesJob{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_jobs",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = make([]*essmsgpb.KubernetesJob, 9)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "jobset_no_jobs",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = []*essmsgpb.KubernetesJob{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_replicas_too_low",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Replicas = 0
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_nil",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_docker_image_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.DockerImage = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_resources_nil",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_resources_resources_nil",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "cpu_millicores_too_low",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{
						CpuMillicores: 0,
						MemoryBytes:   1,
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "memory_bytes_too_low",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{
						CpuMillicores: 1,
						MemoryBytes:   0,
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "gpu_type_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_GpuResources{
					GpuResources: &essmsgpb.GpuResources{
						Type:  "",
						Count: 1,
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "gpu_count_too_low",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_GpuResources{
					GpuResources: &essmsgpb.GpuResources{
						Type:  "gpu",
						Count: 0,
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tpu_type_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_TpuResources{
					TpuResources: &essmsgpb.TpuResources{
						Type:     "",
						Topology: "1x1",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tpu_topology_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_TpuResources{
					TpuResources: &essmsgpb.TpuResources{
						Type:     "tpu",
						Topology: "",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tpu_topology_invalid",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = &essmsgpb.ResourceSet_TpuResources{
					TpuResources: &essmsgpb.TpuResources{
						Type:     "tpu",
						Topology: "invalid-topology",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name:    "valid_request",
			req:     validReq,
			wantErr: nil,
		},
		{
			name: "job_name_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = strings.Repeat("a", 64)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_invalid_format",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = "-job1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_duplicate",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				jobs := r.WorkUnit.Executable.GetJobset().Jobs
				r.WorkUnit.Executable.GetJobset().Jobs = append(jobs, proto.Clone(jobs[0]).(*essmsgpb.KubernetesJob))
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_name_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_self",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "job1",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_not_found",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "job2",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_state_unspecified",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				jobs := r.WorkUnit.Executable.GetJobset().Jobs
				job2 := proto.Clone(jobs[0]).(*essmsgpb.KubernetesJob)
				job2.Name = "job2"
				job2.DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName:            "job1",
					JobDependencyState: essmsgpb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED,
				}
				r.WorkUnit.Executable.GetJobset().Jobs = append(jobs, job2)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_dependency",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				jobs := r.WorkUnit.Executable.GetJobset().Jobs
				job2 := proto.Clone(jobs[0]).(*essmsgpb.KubernetesJob)
				job2.Name = "job2"
				job2.DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName:            "job1",
					JobDependencyState: essmsgpb.KubernetesJob_DependencyOrder_READY,
				}
				r.WorkUnit.Executable.GetJobset().Jobs = append(jobs, job2)
			}),
			wantErr: nil,
		},
		{
			name: "valid_service_account",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = "my-service-account"
			}),
			wantErr: nil,
		},
		{
			name: "invalid_service_account_format",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = "invalid_name"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "service_account_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = strings.Repeat("a", 254)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_priority_class_name",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].PriorityClassName = "very-high"
			}),
			wantErr: nil,
		},
		{
			name: "valid_priority_class_name_system_cluster_critical",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].PriorityClassName = "system-cluster-critical"
			}),
			wantErr: nil,
		},
		{
			name: "invalid_priority_class_name_format",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].PriorityClassName = "INVALID_UPPERCASE"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_priority_class_name_prefix",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].PriorityClassName = "-invalid-prefix"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "priority_class_name_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].PriorityClassName = strings.Repeat("a", 254)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_job_with_sidecars",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Name = "main-container"
				r.WorkUnit.Executable.GetJobset().Jobs[0].Sidecars = []*essmsgpb.ExecutableSpec{
					{
						Name:        "sidecar-container",
						DockerImage: "sidecar-image",
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
								CpuMillicores: 1,
								MemoryBytes:   1,
							}},
						},
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "valid_with_init_containers",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Name = "main-container"
				r.WorkUnit.Executable.GetJobset().Jobs[0].InitContainers = []*essmsgpb.ExecutableSpec{
					{
						Name:        "init-container",
						DockerImage: "init-image",
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
								CpuMillicores: 1,
								MemoryBytes:   1,
							}},
						},
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "invalid_sidecar_duplicate_container_name",
			req: proto.Modify(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Name = "same-name"
				r.WorkUnit.Executable.GetJobset().Jobs[0].Sidecars = []*essmsgpb.ExecutableSpec{
					{
						Name:        "same-name",
						DockerImage: "sidecar-image",
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
								CpuMillicores: 1,
								MemoryBytes:   1,
							}},
						},
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_node_selector",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/gke-nodepool": "cpups",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: nil,
		},
		{
			name: "invalid_node_selector_empty_key",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"": "cpups",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_value_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/gke-nodepool": strings.Repeat("a", 64),
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_key_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										strings.Repeat("a", 317): "cpups",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_node_selector_no_slash_uppercase_dots",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"My_Label.1": "Valid-Value_1.0",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: nil,
		},
		{
			name: "invalid_node_selector_value_invalid_chars",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/gke-nodepool": "invalid@value",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_multiple_slashes",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"a/b/c": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_empty_prefix",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"/name": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_prefix_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										strings.Repeat("a", 254) + "/name": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_invalid_prefix",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"Invalid_Prefix.com/name": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_empty_name",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_name_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/" + strings.Repeat("a", 64): "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_node_selector_invalid_name",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{
								{
									Name:     "job1",
									Replicas: 1,
									Spec: &essmsgpb.ExecutableSpec{
										Name:        "primary",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									NodeSelector: map[string]string{
										"cloud.google.com/name@invalid": "val",
									},
								},
							},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateWorkUnitRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateWorkUnitRequest(%v) returned unexpected error: %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

var baseWorkUnitReqWithSuccessPolicy = &essapipb.CreateWorkUnitRequest{
	Parent: "experiments/123",
	WorkUnit: &essmsgpb.WorkUnit{
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: "queue"},
			Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
				Jobs: []*essmsgpb.KubernetesJob{{
					Name:     "pathways-head",
					Replicas: 1,
					Spec: &essmsgpb.ExecutableSpec{
						DockerImage: "image",
						Resources: &essmsgpb.ResourceSet{
							Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{CpuMillicores: 1, MemoryBytes: 1}},
						},
					},
				}},
			}},
		},
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
	},
}

func createValidReqWithSuccessPolicy(policy *essmsgpb.JobSetExecutable_SuccessPolicy) *essapipb.CreateWorkUnitRequest {
	return proto.Modify(baseWorkUnitReqWithSuccessPolicy, func(req *essapipb.CreateWorkUnitRequest) {
		req.GetWorkUnit().GetExecutable().GetJobset().SuccessPolicy = policy
	})
}

func TestValidateCreateWorkUnitRequest_SuccessPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  *essmsgpb.JobSetExecutable_SuccessPolicy
		wantErr error
	}{
		{"valid_policy", &essmsgpb.JobSetExecutable_SuccessPolicy{Operator: essmsgpb.JobSetExecutable_SuccessPolicy_ALL, TargetReplicatedJobs: []string{"pathways-head"}}, nil},
		{"empty_target", &essmsgpb.JobSetExecutable_SuccessPolicy{TargetReplicatedJobs: []string{""}}, status.ErrInvalidArgument},
		{"unknown_job", &essmsgpb.JobSetExecutable_SuccessPolicy{TargetReplicatedJobs: []string{"non-existent"}}, status.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateWorkUnitRequest(createValidReqWithSuccessPolicy(tc.policy))
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateWorkUnitRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateWorkUnitRequest_DNSPolicy(t *testing.T) {
	tests := []struct {
		name      string
		dnsPolicy string
		wantErr   error
	}{
		{"valid_cluster_first", "ClusterFirst", nil},
		{"valid_cluster_first_with_host_net", "ClusterFirstWithHostNet", nil},
		{"valid_default", "Default", nil},
		{"valid_none", "None", nil},
		{"invalid_dns_policy", "InvalidPolicy", status.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := proto.Modify(baseWorkUnitReqWithSuccessPolicy, func(req *essapipb.CreateWorkUnitRequest) {
				req.GetWorkUnit().GetExecutable().GetJobset().GetJobs()[0].DnsPolicy = proto.String(tc.dnsPolicy)
			})
			err := ValidateCreateWorkUnitRequest(req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateWorkUnitRequest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateCreateArtifactRequest(t *testing.T) {
	validReq := &essapipb.CreateArtifactRequest{
		Parent: "experiments/123",
		Artifact: &essmsgpb.Artifact{
			Url: &essmsgpb.Url{
				Url: "http://example.com",
			},
			Payload: &essmsgpb.ArtifactPayload{
				Type: &essmsgpb.ArtifactPayload_Text{
					Text: &essmsgpb.TextArtifact{
						Title: "title",
						Text:  "text",
					},
				},
			},
		},
	}

	tests := []struct {
		name    string
		req     *essapipb.CreateArtifactRequest
		wantErr error
	}{
		{
			name:    "nil_request",
			req:     nil,
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_parent",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Parent = "invalid/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_artifact",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Name = "artifacts/1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "parent_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Parent = "experiments/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_url_non_managed_xprof",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_url_non_managed_xprof",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = &essmsgpb.Url{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			// EXECUTION phase alone must NOT exempt the URL requirement; only a
			// ManagedXprof payload does.
			name: "execution_phase_does_not_exempt_url",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.LifecyclePhase = essmsgpb.Artifact_EXECUTION
				r.Artifact.Url = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "managed_xprof_no_url_ok",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = nil
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "display_name",
						RunGroup:    "run_group",
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "url_url_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = &essmsgpb.Url{
					Url: strings.Repeat("a", maxURLLen+1),
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_missing",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload = nil
			}),
			wantErr: nil,
		},
		{
			name:    "valid_request_exp_parent",
			req:     validReq,
			wantErr: nil,
		},
		{
			name: "valid_request_wu_parent",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Parent = "experiments/123/workUnits/1"
			}),
			wantErr: nil,
		},
		{
			name: "payload_type_nil",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload = &essmsgpb.ArtifactPayload{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_managed_xprof_missing_display_name",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						RunGroup: "run_group",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_managed_xprof_missing_run_group",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "display_name",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_code_source_missing_title",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_CodeSource{
					CodeSource: &essmsgpb.CodeSourceArtifact{
						CodeBlock: "code_block",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_code_source_missing_code_block",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_CodeSource{
					CodeSource: &essmsgpb.CodeSourceArtifact{
						Title: "title",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_text_missing_title",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Text{
					Text: &essmsgpb.TextArtifact{
						Text: "text",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_text_missing_text",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Text{
					Text: &essmsgpb.TextArtifact{
						Title: "title",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_managed_xprof_payload",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_ManagedXprof{
					ManagedXprof: &essmsgpb.ManagedXprofArtifact{
						DisplayName: "display_name",
						RunGroup:    "run_group",
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "valid_request_with_code_source_payload",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_CodeSource{
					CodeSource: &essmsgpb.CodeSourceArtifact{
						Title:     "title",
						CodeBlock: "code_block",
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "valid_request_with_text_payload",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Text{
					Text: &essmsgpb.TextArtifact{
						Title: "title",
						Text:  "text",
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "payload_url_missing_url",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Url{
					Url: &essmsgpb.UrlArtifact{
						DisplayName: "Example Link",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_url_payload",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Url{
					Url: &essmsgpb.UrlArtifact{
						Url:         "http://example.com",
						DisplayName: "Example Link",
					},
				}
			}),
			wantErr: nil,
		},
		{
			name: "payload_chart_missing_metrics",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Chart{
					Chart: &essmsgpb.ChartArtifact{
						Title: "Test Chart",
					},
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_chart_payload",
			req: proto.Modify(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload.Type = &essmsgpb.ArtifactPayload_Chart{
					Chart: &essmsgpb.ChartArtifact{
						Title:       "Test Chart",
						MetricNames: []string{"accuracy"},
					},
				}
			}),
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateArtifactRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateArtifactRequest(%v) returned unexpected error: %v, want %v", tc.req, err, tc.wantErr)
			}
		})
	}
}

func TestValidateExecutableSpec_RFC1123Name(t *testing.T) {
	validRes := &essmsgpb.ResourceSet{
		Resources: &essmsgpb.ResourceSet_CpuResources{
			CpuResources: &essmsgpb.CpuResources{
				CpuMillicores: 100,
				MemoryBytes:   1024,
			},
		},
	}
	tests := []struct {
		name    string
		spec    *essmsgpb.ExecutableSpec
		wantErr bool
	}{
		{
			name: "valid_name",
			spec: &essmsgpb.ExecutableSpec{
				Name:        "worker-1",
				DockerImage: "gcr.io/test",
				Resources:   validRes,
			},
			wantErr: false,
		},
		{
			name: "invalid_name_uppercase",
			spec: &essmsgpb.ExecutableSpec{
				Name:        "Worker_1",
				DockerImage: "gcr.io/test",
				Resources:   validRes,
			},
			wantErr: true,
		},
		{
			name: "invalid_name_too_long",
			spec: &essmsgpb.ExecutableSpec{
				Name:        "a1234567890123456789012345678901234567890123456789012345678901234",
				DockerImage: "gcr.io/test",
				Resources:   validRes,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateExecutableSpec(tc.spec, "spec")
			if (err != nil) != tc.wantErr {
				t.Errorf("validateExecutableSpec() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateContainerNamesUnique_ResolvedSidecarName(t *testing.T) {
	tests := []struct {
		name    string
		job     *essmsgpb.KubernetesJob
		wantErr bool
	}{
		{
			name: "valid_resolved_sidecar_name",
			job: &essmsgpb.KubernetesJob{
				Name: "worker",
				Spec: &essmsgpb.ExecutableSpec{Name: "main"},
				Sidecars: []*essmsgpb.ExecutableSpec{
					{},
				},
			},
			wantErr: false,
		},
		{
			name: "resolved_default_sidecar_name_too_long",
			job: &essmsgpb.KubernetesJob{
				Name: "a1234567890123456789012345678901234567890123456789012345678",
				Spec: &essmsgpb.ExecutableSpec{Name: "main"},
				Sidecars: []*essmsgpb.ExecutableSpec{
					{},
				},
			},
			wantErr: true,
		},
		{
			name: "valid_init_container_name",
			job: &essmsgpb.KubernetesJob{
				Name: "worker",
				Spec: &essmsgpb.ExecutableSpec{Name: "main"},
				InitContainers: []*essmsgpb.ExecutableSpec{
					{Name: "init-db"},
				},
			},
			wantErr: false,
		},
		{
			name: "init_container_name_conflicts_with_primary",
			job: &essmsgpb.KubernetesJob{
				Name: "worker",
				Spec: &essmsgpb.ExecutableSpec{Name: "main"},
				InitContainers: []*essmsgpb.ExecutableSpec{
					{Name: "main"},
				},
			},
			wantErr: true,
		},
		{
			name: "init_container_name_conflicts_with_sidecar",
			job: &essmsgpb.KubernetesJob{
				Name: "worker",
				Spec: &essmsgpb.ExecutableSpec{Name: "main"},
				Sidecars: []*essmsgpb.ExecutableSpec{
					{Name: "sidecar-a"},
				},
				InitContainers: []*essmsgpb.ExecutableSpec{
					{Name: "sidecar-a"},
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateContainerNamesUnique(tc.job)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateContainerNamesUnique() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateJobSetNetwork(t *testing.T) {
	tests := []struct {
		name    string
		network *essmsgpb.JobSetExecutable_Network
		wantErr bool
	}{
		{
			name:    "nil network",
			network: nil,
			wantErr: false,
		},
		{
			name: "valid subdomain",
			network: &essmsgpb.JobSetExecutable_Network{
				Subdomain: "valid-subdomain-123",
			},
			wantErr: false,
		},
		{
			name: "invalid subdomain with uppercase",
			network: &essmsgpb.JobSetExecutable_Network{
				Subdomain: "Invalid-Subdomain",
			},
			wantErr: true,
		},
		{
			name: "invalid subdomain with special characters",
			network: &essmsgpb.JobSetExecutable_Network{
				Subdomain: "invalid_subdomain",
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJobSetNetwork(tc.network)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateJobSetNetwork() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
func TestValidateTPUTopology(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		wantErr  bool
	}{
		{name: "valid 2x2", topology: "2x2", wantErr: false},
		{name: "valid 2x2x4", topology: "2x2x4", wantErr: false},
		{name: "valid 8x8", topology: "8x8", wantErr: false},
		{name: "valid single dimension", topology: "4", wantErr: false},
		{name: "empty topology", topology: "", wantErr: true},
		{name: "invalid characters", topology: "abc", wantErr: true},
		{name: "negative dimension", topology: "-2x2", wantErr: true},
		{name: "zero dimension", topology: "0x4", wantErr: true},
		{name: "trailing x", topology: "2x", wantErr: true},
		{name: "leading x", topology: "x2", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTPUTopology(tc.topology, "prefix")
			if (err != nil) != tc.wantErr {
				t.Errorf("validateTPUTopology(%q) error = %v, wantErr %v", tc.topology, err, tc.wantErr)
			}
		})
	}
}

func TestValidateJobSetCoordinator(t *testing.T) {
	jobs := map[string]*essmsgpb.KubernetesJob{
		"worker": {
			Name:     "worker",
			Replicas: 2,
		},
	}

	tests := []struct {
		name        string
		coordinator *essmsgpb.JobSetExecutable_Coordinator
		wantErr     bool
	}{
		{
			name:        "nil coordinator",
			coordinator: nil,
			wantErr:     false,
		},
		{
			name: "valid coordinator (default indexes)",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "worker",
			},
			wantErr: false,
		},
		{
			name: "valid coordinator (explicit indexes)",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "worker",
				JobIndex:      proto.Int32(1),
				PodIndex:      proto.Int32(0),
			},
			wantErr: false,
		},
		{
			name: "empty replicated_job",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "",
			},
			wantErr: true,
		},
		{
			name: "unknown replicated_job",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "non-existent-job",
			},
			wantErr: true,
		},
		{
			name: "negative job_index",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "worker",
				JobIndex:      proto.Int32(-1),
			},
			wantErr: true,
		},
		{
			name: "out-of-bounds job_index",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "worker",
				JobIndex:      proto.Int32(2),
			},
			wantErr: true,
		},
		{
			name: "negative pod_index",
			coordinator: &essmsgpb.JobSetExecutable_Coordinator{
				ReplicatedJob: "worker",
				PodIndex:      proto.Int32(-1),
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateJobSetCoordinator(tc.coordinator, jobs)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateJobSetCoordinator() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
