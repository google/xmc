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
package spanner

import (
	"context"
	"fmt"
	"testing"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func BlankExperimentProto(xid int64) *essmsgpb.Experiment {
	return &essmsgpb.Experiment{
		Name:    fmt.Sprintf("experiments/%d", xid),
		Title:   "test-title",
		Creator: "test@example.com",
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				{
					Principal: &xidpb.XidAcls_Role_AllAuthenticatedUsers{
						AllAuthenticatedUsers: true,
					},
				},
			},
		},
		Priority:  1,
		Urls:      []*essmsgpb.Url{},
		Tags:      []string{},
		Notes:     "",
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}
}

func CreateAndStoreExperiment(ctx context.Context, t *testing.T, storage *Storage, exp *essmsgpb.Experiment) *essmsgpb.Experiment {
	t.Helper()
	createdExp, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}
	return createdExp
}

func CreateBlankExperiment(ctx context.Context, t *testing.T, storage *Storage, xid int64) *essmsgpb.Experiment {
	t.Helper()
	exp := BlankExperimentProto(xid)
	return CreateAndStoreExperiment(ctx, t, storage, exp)
}

func CreateBlankExperimentWithLaunchState(ctx context.Context, t *testing.T, storage *Storage, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) *essmsgpb.Experiment {
	t.Helper()
	exp := BlankExperimentProto(xid)
	exp.Status.LaunchState = launchState
	return CreateAndStoreExperiment(ctx, t, storage, exp)
}

func BlankWorkUnitProto(xid int64, wid int64) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Name:         fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		Urls: []*essmsgpb.Url{
			{
				Url: "http://test.com",
			},
		},
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{
				QueueName: "test-queue",
			},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test-notes",
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: fmt.Sprintf("experiments/%d/workUnits/%d/executions/1", xid, wid),
			Status: &essmsgpb.WorkUnitStatus{
				State:           essmsgpb.WorkUnitStatus_INITIALIZING,
				StateUpdateTime: timestamppb.New(time.Now()),
				SystemMessage: &essmsgpb.StructuredMessage{
					Severity:       essmsgpb.StructuredMessage_INFO,
					Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
					Parent:         fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid),
					UserFacingText: "test-message",
					CreateTime:     timestamppb.New(time.Now()),
				},
			},
			State:    essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{},
		},
	}
}

func CreateAndStoreWorkUnit(ctx context.Context, t *testing.T, storage *Storage, wu *essmsgpb.WorkUnit) *essmsgpb.WorkUnit {
	t.Helper()
	createdWu, err := storage.CreateWorkUnit(ctx, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}
	return createdWu
}

func CreateBlankWorkUnit(ctx context.Context, t *testing.T, storage *Storage, xid int64, wid int64) *essmsgpb.WorkUnit {
	t.Helper()
	wu := BlankWorkUnitProto(xid, wid)
	return CreateAndStoreWorkUnit(ctx, t, storage, wu)
}

func CreateBlankWorkUnitWithState(ctx context.Context, t *testing.T, storage *Storage, xid int64, wid int64, state essmsgpb.WorkUnitStatus_WorkUnitState) *essmsgpb.WorkUnit {
	t.Helper()
	wu := BlankWorkUnitProto(xid, wid)
	wu.CurrentWorkUnitExecution.Status.State = state
	return CreateAndStoreWorkUnit(ctx, t, storage, wu)
}

func ChildStateCountInState(t *testing.T, exp *essmsgpb.Experiment, state essmsgpb.WorkUnitStatus_WorkUnitState) int64 {
	t.Helper()
	for _, count := range exp.Status.ChildStateCounts {
		if count.State == state {
			return count.Count
		}
	}
	return 0
}

func MessageSeverityCountInState(t *testing.T, exp *essmsgpb.Experiment, severity essmsgpb.StructuredMessage_Severity) int64 {
	t.Helper()
	for _, severityCount := range exp.Status.MessageSeverityCounts {
		if severityCount.GetSeverity() == severity {
			return severityCount.GetCount()
		}
	}
	return 0
}

func BlankArtifactProto(xid, aid int64) *essmsgpb.Artifact {
	return &essmsgpb.Artifact{
		Name:   fmt.Sprintf("artifacts/%d", aid),
		Parent: fmt.Sprintf("experiments/%d", xid),
		Url: &essmsgpb.Url{
			Url: fmt.Sprintf("http://test-%d.com", aid),
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "test-notes",
	}
}

func CreateAndStoreArtifact(ctx context.Context, t *testing.T, storage *Storage, art *essmsgpb.Artifact) *essmsgpb.Artifact {
	t.Helper()
	createdArt, err := storage.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact(%v) failed: %v", art, err)
	}
	return createdArt
}
