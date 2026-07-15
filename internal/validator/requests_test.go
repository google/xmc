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
	"google.golang.org/protobuf/proto"
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
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Name = proto.String("xids/123")
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_acls",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_acls",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls = &xidmessagespb.XidAcls{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_origin",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Origin = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_owner_role",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls.Owners = []*xidmessagespb.XidAcls_Role{{}}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_writer_role",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.Acls.Writers = []*xidmessagespb.XidAcls_Role{{}}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_reader_role",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
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
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/123")
			}),
			wantErr: nil,
		},
		{
			name: "valid_parent_xid_nil_acls",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
				r.XidEntry.ParentXid = proto.String("xids/123")
				r.XidEntry.Acls = nil
			}),
			wantErr: nil,
		},
		{
			name: "invalid_parent_xid",
			req: modifyProto(validReq, func(r *xidapipb.CreateXidEntryRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Name = "experiments/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "creator_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Creator = "user"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Title = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Title = " "
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "status_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Status = &essmsgpb.ExperimentStatus{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
				r.Experiment.Urls = make([]*essmsgpb.Url, maxURLs+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateExperimentRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.UpdateExperimentRequest) {
				r.Experiment.Title = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: modifyProto(validReq, func(r *essapipb.UpdateExperimentRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.UpdateExperimentRequest) {
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
	art := &essmsgpb.Artifact{}
	got := FillArtifactNullValues(art)
	want := &essmsgpb.Artifact{
		Tags: []string{},
	}
	if diff := cmp.Diff(got, want, protocmp.Transform()); diff != "" {
		t.Errorf("FillArtifactNullValues(%v) returned unexpected diff (-got +want):\n%s", art, diff)
	}
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.Parent = "invalid/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_work_unit",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Name = "experiments/123/workUnits/1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "current_work_unit_execution_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.CurrentWorkUnitExecution = &essmsgpb.WorkUnitExecution{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Urls = make([]*essmsgpb.Url, maxURLs+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "work_unit_type_unspecified",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.WorkUnitType = essmsgpb.WorkUnit_WORK_UNIT_TYPE_UNSPECIFIED
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_scheduling_constraints",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_queue_name",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints.QueueName = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "queue_name_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.SchedulingConstraints.QueueName = strings.Repeat("a", maxQueueNameLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_jobset",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.Type = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "no_jobs",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = []*essmsgpb.KubernetesJob{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_jobs",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = make([]*essmsgpb.KubernetesJob, 9)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "jobset_no_jobs",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs = []*essmsgpb.KubernetesJob{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_replicas_too_low",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Replicas = 0
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_nil",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_docker_image_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.DockerImage = ""
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_resources_nil",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_resources_resources_nil",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Spec.Resources.Resources = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "cpu_millicores_too_low",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			name:    "valid_request",
			req:     validReq,
			wantErr: nil,
		},
		{
			name: "job_name_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = strings.Repeat("a", 64)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_invalid_format",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].Name = "-job1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_duplicate",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				jobs := r.WorkUnit.Executable.GetJobset().Jobs
				r.WorkUnit.Executable.GetJobset().Jobs = append(jobs, proto.Clone(jobs[0]).(*essmsgpb.KubernetesJob))
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_name_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_self",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "job1",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_not_found",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].DependencyOrder = &essmsgpb.KubernetesJob_DependencyOrder{
					JobName: "job2",
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_state_unspecified",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = "my-service-account"
			}),
			wantErr: nil,
		},
		{
			name: "invalid_service_account_format",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = "invalid_name"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "service_account_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
				r.WorkUnit.Executable.GetJobset().Jobs[0].ServiceAccount = strings.Repeat("a", 254)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_job_with_sidecars",
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateWorkUnitRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Parent = "invalid/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_artifact",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Name = "artifacts/1"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "parent_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Parent = "experiments/123"
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.CreateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.UpdateTime = timestamppb.New(time.Now())
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_url",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = nil
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_url_url",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = &essmsgpb.Url{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_url_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Url = &essmsgpb.Url{
					Url: strings.Repeat("a", maxURLLen+1),
				}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Tags = make([]string, maxTags+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Tags = []string{strings.Repeat("a", maxTagLen+1)}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Notes = strings.Repeat("a", maxNotesLen+1)
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_missing",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Parent = "experiments/123/workUnits/1"
			}),
			wantErr: nil,
		},
		{
			name: "payload_type_nil",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
				r.Artifact.Payload = &essmsgpb.ArtifactPayload{}
			}),
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_managed_xprof_missing_display_name",
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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
			req: modifyProto(validReq, func(r *essapipb.CreateArtifactRequest) {
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

func modifyProto[T proto.Message](m T, f func(T)) T {
	cloned := proto.Clone(m).(T)
	f(cloned)
	return cloned
}
