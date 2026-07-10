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
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Name: proto.String("xids/123"),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_acls",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_acls",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_origin",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Owners: []*xidmessagespb.XidAcls_Role{
							{Principal: &xidmessagespb.XidAcls_Role_Group{Group: "group"}},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_owner_role",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Owners: []*xidmessagespb.XidAcls_Role{
							{},
						},
					},
					Origin: &xidmessagespb.OriginatingSystem{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_writer_role",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Writers: []*xidmessagespb.XidAcls_Role{
							{},
						},
					},
					Origin: &xidmessagespb.OriginatingSystem{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "invalid_reader_role",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Readers: []*xidmessagespb.XidAcls_Role{
							{},
						},
					},
					Origin: &xidmessagespb.OriginatingSystem{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Owners: []*xidmessagespb.XidAcls_Role{
							{Principal: &xidmessagespb.XidAcls_Role_Group{Group: "group"}},
						},
					},
					Origin: &xidmessagespb.OriginatingSystem{},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_parent_xid",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Owners: []*xidmessagespb.XidAcls_Role{
							{Principal: &xidmessagespb.XidAcls_Role_Group{Group: "group"}},
						},
					},
					Origin:    &xidmessagespb.OriginatingSystem{},
					ParentXid: proto.String("xids/123"),
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_parent_xid_nil_acls",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Origin:    &xidmessagespb.OriginatingSystem{},
					ParentXid: proto.String("xids/123"),
				},
			},
			wantErr: nil,
		},
		{
			name: "invalid_parent_xid",
			req: &xidapipb.CreateXidEntryRequest{
				XidEntry: &xidmessagespb.XidEntry{
					Acls: &xidmessagespb.XidAcls{
						Owners: []*xidmessagespb.XidAcls_Role{
							{Principal: &xidmessagespb.XidAcls_Role_Group{Group: "group"}},
						},
					},
					Origin:    &xidmessagespb.OriginatingSystem{},
					ParentXid: proto.String("invalid/123"),
				},
			},
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
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Name: "experiments/123",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "creator_not_empty",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Creator: "user",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_empty",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: " ",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title:      "title",
					CreateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title:      "title",
					UpdateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "status_not_empty",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title:  "title",
					Status: &essmsgpb.ExperimentStatus{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Tags:  make([]string, maxTags+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Tags:  []string{strings.Repeat("a", maxTagLen+1)},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Notes: strings.Repeat("a", maxNotesLen+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Urls:  make([]*essmsgpb.Url, maxURLs+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Urls: []*essmsgpb.Url{
						{
							Url: strings.Repeat("a", maxURLLen+1),
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_acls",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request",
			req: &essapipb.CreateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
					Acls:  &xidmessagespb.XidAcls{},
				},
			},
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
	tests := []struct {
		name    string
		req     *essapipb.UpdateExperimentRequest
		wantErr error
	}{
		{
			name: "title_empty",
			req: &essapipb.UpdateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "",
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "title_whitespace",
			req: &essapipb.UpdateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: " ",
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request",
			req: &essapipb.UpdateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "title",
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
			},
			wantErr: nil,
		},
		{
			name: "title_empty_no_mask",
			req: &essapipb.UpdateExperimentRequest{
				Experiment: &essmsgpb.Experiment{
					Title: "",
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{}},
			},
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
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "invalid/123",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_work_unit",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Name: "experiments/123/workUnits/1",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					CreateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					UpdateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "current_work_unit_execution_not_empty",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Tags: make([]string, maxTags+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Tags: []string{strings.Repeat("a", maxTagLen+1)},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Notes: strings.Repeat("a", maxNotesLen+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_urls",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Urls: make([]*essmsgpb.Url, maxURLs+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Urls: []*essmsgpb.Url{
						{
							Url: strings.Repeat("a", maxURLLen+1),
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_executable",
			req: &essapipb.CreateWorkUnitRequest{
				Parent:   "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "work_unit_type_unspecified",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_scheduling_constraints",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable:   &essmsgpb.Executable{},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_queue_name",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "queue_name_too_long",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: strings.Repeat("a", maxQueueNameLen+1),
						},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_jobset",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "no_jobs",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_jobs",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: make([]*essmsgpb.KubernetesJob, 9),
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "jobset_no_jobs",
			req: &essapipb.CreateWorkUnitRequest{
				Parent: "experiments/123",
				WorkUnit: &essmsgpb.WorkUnit{
					Executable: &essmsgpb.Executable{
						SchedulingConstraints: &essmsgpb.SchedulingConstraints{
							QueueName: "queue",
						},
						Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{
							Jobs: []*essmsgpb.KubernetesJob{},
						}},
					},
					WorkUnitType: essmsgpb.WorkUnit_STANDARD,
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_replicas_too_low",
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
									Replicas: 0,
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_nil",
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
									Spec:     nil,
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
			name: "job_name_empty",
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
									Name:     "",
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_docker_image_empty",
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
										DockerImage: "",
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_spec_resources_nil",
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
										DockerImage: "image",
										Resources:   nil,
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
			name: "job_spec_resources_resources_nil",
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
										DockerImage: "image",
										Resources:   &essmsgpb.ResourceSet{},
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
			name: "cpu_millicores_too_low",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 0,
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "memory_bytes_too_low",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   0,
											}},
										},
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
			name: "gpu_type_empty",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_GpuResources{GpuResources: &essmsgpb.GpuResources{
												Type:  "",
												Count: 1,
											}},
										},
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
			name: "gpu_count_too_low",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_GpuResources{GpuResources: &essmsgpb.GpuResources{
												Type:  "gpu",
												Count: 0,
											}},
										},
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
			name: "tpu_type_empty",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_TpuResources{TpuResources: &essmsgpb.TpuResources{
												Type:     "",
												Topology: "1x1",
											}},
										},
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
			name: "tpu_topology_empty",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_TpuResources{TpuResources: &essmsgpb.TpuResources{
												Type:     "tpu",
												Topology: "",
											}},
										},
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
			name: "valid_request",
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
			},
			wantErr: nil,
		},
		{
			name: "job_name_too_long",
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
									Name:     strings.Repeat("a", 64),
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_invalid_format",
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
									Name:     "-job1",
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_name_duplicate",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
								},
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
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "job_dependency_name_empty",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									DependencyOrder: &essmsgpb.KubernetesJob_DependencyOrder{
										JobName: "",
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
			name: "job_dependency_self",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									DependencyOrder: &essmsgpb.KubernetesJob_DependencyOrder{
										JobName: "job1",
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
			name: "job_dependency_not_found",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									DependencyOrder: &essmsgpb.KubernetesJob_DependencyOrder{
										JobName: "job2",
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
			name: "job_dependency_state_unspecified",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
								},
								{
									Name:     "job2",
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
									DependencyOrder: &essmsgpb.KubernetesJob_DependencyOrder{
										JobName:            "job1",
										JobDependencyState: essmsgpb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED,
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
			name: "valid_request_with_dependency",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
								},
								{
									Name:     "job2",
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
									DependencyOrder: &essmsgpb.KubernetesJob_DependencyOrder{
										JobName:            "job1",
										JobDependencyState: essmsgpb.KubernetesJob_DependencyOrder_READY,
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
			name: "valid_service_account",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									ServiceAccount: "my-service-account",
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
			name: "invalid_service_account_format",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									ServiceAccount: "invalid_name",
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
			name: "service_account_too_long",
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
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									ServiceAccount: strings.Repeat("a", 254),
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
			name: "valid_job_with_sidecars",
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
										Name:        "main-container",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									Sidecars: []*essmsgpb.ExecutableSpec{
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
			name: "valid_with_init_containers",
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
										Name:        "main-container",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									InitContainers: []*essmsgpb.ExecutableSpec{
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
			name: "invalid_sidecar_duplicate_container_name",
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
										Name:        "same-name",
										DockerImage: "image",
										Resources: &essmsgpb.ResourceSet{
											Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{
												CpuMillicores: 1,
												MemoryBytes:   1,
											}},
										},
									},
									Sidecars: []*essmsgpb.ExecutableSpec{
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

func TestValidateCreateArtifactRequest(t *testing.T) {
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
			req: &essapipb.CreateArtifactRequest{
				Parent: "invalid/123",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_artifact",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "name_not_empty",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Name: "artifacts/1",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "parent_not_empty",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Parent: "experiments/123",
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "create_time_not_empty",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					CreateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "update_time_not_empty",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					UpdateTime: timestamppb.New(time.Now()),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "nil_url",
			req: &essapipb.CreateArtifactRequest{
				Parent:   "experiments/123",
				Artifact: &essmsgpb.Artifact{},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "empty_url_url",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "url_url_too_long",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{
						Url: strings.Repeat("a", maxURLLen+1),
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "too_many_tags",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{
						Url: "http://example.com",
					},
					Tags: make([]string, maxTags+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "tag_too_long",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{
						Url: "http://example.com",
					},
					Tags: []string{strings.Repeat("a", maxTagLen+1)},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "notes_too_long",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{
						Url: "http://example.com",
					},
					Notes: strings.Repeat("a", maxNotesLen+1),
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_missing",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{
						Url: "http://example.com",
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_exp_parent",
			req: &essapipb.CreateArtifactRequest{
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
			},
			wantErr: nil,
		},
		{
			name: "valid_request_wu_parent",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123/workUnits/1",
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
			},
			wantErr: nil,
		},
		{
			name: "payload_type_nil",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url:     &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_managed_xprof_missing_display_name",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_ManagedXprof{
							ManagedXprof: &essmsgpb.ManagedXprofArtifact{
								RunGroup: "run_group",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_managed_xprof_missing_run_group",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_ManagedXprof{
							ManagedXprof: &essmsgpb.ManagedXprofArtifact{
								DisplayName: "display_name",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_code_source_missing_title",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_CodeSource{
							CodeSource: &essmsgpb.CodeSourceArtifact{
								CodeBlock: "code_block",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_code_source_missing_code_block",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_CodeSource{
							CodeSource: &essmsgpb.CodeSourceArtifact{
								Title: "title",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_text_missing_title",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_Text{
							Text: &essmsgpb.TextArtifact{
								Text: "text",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "payload_text_missing_text",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_Text{
							Text: &essmsgpb.TextArtifact{
								Title: "title",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_managed_xprof_payload",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_ManagedXprof{
							ManagedXprof: &essmsgpb.ManagedXprofArtifact{
								DisplayName: "display_name",
								RunGroup:    "run_group",
							},
						},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_code_source_payload",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_CodeSource{
							CodeSource: &essmsgpb.CodeSourceArtifact{
								Title:     "title",
								CodeBlock: "code_block",
							},
						},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "valid_request_with_text_payload",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_Text{
							Text: &essmsgpb.TextArtifact{
								Title: "title",
								Text:  "text",
							},
						},
					},
				},
			},
			wantErr: nil,
		},
		{
			name: "payload_url_missing_url",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_Url{
							Url: &essmsgpb.UrlArtifact{
								DisplayName: "Example Link",
							},
						},
					},
				},
			},
			wantErr: status.ErrInvalidArgument,
		},
		{
			name: "valid_request_with_url_payload",
			req: &essapipb.CreateArtifactRequest{
				Parent: "experiments/123",
				Artifact: &essmsgpb.Artifact{
					Url: &essmsgpb.Url{Url: "http://example.com"},
					Payload: &essmsgpb.ArtifactPayload{
						Type: &essmsgpb.ArtifactPayload_Url{
							Url: &essmsgpb.UrlArtifact{
								Url:         "http://example.com",
								DisplayName: "Example Link",
							},
						},
					},
				},
			},
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
