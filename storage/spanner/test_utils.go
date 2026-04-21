package spanner

import (
	"context"
	"fmt"
	"testing"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func blankExperimentProto(xid int64) *essmsgpb.Experiment {
	return &essmsgpb.Experiment{
		Name:    fmt.Sprintf("experiments/%d", xid),
		Title:   "test-title",
		Creator: "test@example.com",
		Acls: &xidpb.XidAcls{
			Owners: []*xidpb.XidAcls_Role{
				&xidpb.XidAcls_Role{
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

func createAndStoreExperiment(ctx context.Context, t *testing.T, storage *Storage, exp *essmsgpb.Experiment) *essmsgpb.Experiment {
	t.Helper()
	createdExp, err := storage.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}
	return createdExp
}

func createBlankExperiment(ctx context.Context, t *testing.T, storage *Storage, xid int64) *essmsgpb.Experiment {
	t.Helper()
	exp := blankExperimentProto(xid)
	return createAndStoreExperiment(ctx, t, storage, exp)
}

func createBlankExperimentWithLaunchState(ctx context.Context, t *testing.T, storage *Storage, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) *essmsgpb.Experiment {
	t.Helper()
	exp := blankExperimentProto(xid)
	exp.Status.LaunchState = launchState
	return createAndStoreExperiment(ctx, t, storage, exp)
}

func blankWorkUnitProto(xid int64, wid int64) *essmsgpb.WorkUnit {
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

func createAndStoreWorkUnit(ctx context.Context, t *testing.T, storage *Storage, wu *essmsgpb.WorkUnit) *essmsgpb.WorkUnit {
	t.Helper()
	createdWu, err := storage.CreateWorkUnit(ctx, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}
	return createdWu
}

func createBlankWorkUnit(ctx context.Context, t *testing.T, storage *Storage, xid int64, wid int64) *essmsgpb.WorkUnit {
	t.Helper()
	wu := blankWorkUnitProto(xid, wid)
	return createAndStoreWorkUnit(ctx, t, storage, wu)
}

func createBlankWorkUnitWithState(ctx context.Context, t *testing.T, storage *Storage, xid int64, wid int64, state essmsgpb.WorkUnitStatus_WorkUnitState) *essmsgpb.WorkUnit {
	t.Helper()
	wu := blankWorkUnitProto(xid, wid)
	wu.CurrentWorkUnitExecution.Status.State = state
	return createAndStoreWorkUnit(ctx, t, storage, wu)
}

func childStateCountInState(t *testing.T, exp *essmsgpb.Experiment, state essmsgpb.WorkUnitStatus_WorkUnitState) int64 {
	t.Helper()
	for _, count := range exp.Status.ChildStateCounts {
		if count.State == state {
			return count.Count
		}
	}
	return 0
}

func messageSeverityCountInState(t *testing.T, exp *essmsgpb.Experiment, severity essmsgpb.StructuredMessage_Severity) int64 {
	t.Helper()
	for _, severityCount := range exp.Status.MessageSeverityCounts {
		if severityCount.GetSeverity() == severity {
			return severityCount.GetCount()
		}
	}
	return 0
}

func blankArtifactProto(xid, aid int64) *essmsgpb.Artifact {
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

func createAndStoreArtifact(ctx context.Context, t *testing.T, storage *Storage, art *essmsgpb.Artifact) *essmsgpb.Artifact {
	t.Helper()
	createdArt, err := storage.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact(%v) failed: %v", art, err)
	}
	return createdArt
}
