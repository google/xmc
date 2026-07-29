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

package testing_test

import (
	"context"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	storagetesting "xmanager_cloud/internal/storage/testing"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestFakeStorage_ExperimentCRUD(t *testing.T) {
	ctx := context.Background()
	fake := storagetesting.NewFakeStorage()

	exp := &essmsgpb.Experiment{
		Name:    "experiments/100",
		Title:   "Test Experiment",
		Creator: "test@example.com",
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}

	created, err := fake.CreateExperiment(ctx, exp)
	if err != nil {
		t.Fatalf("CreateExperiment() failed: %v", err)
	}
	if diff := cmp.Diff(exp, created, protocmp.Transform()); diff != "" {
		t.Errorf("CreateExperiment() mismatch (-want +got):\n%s", diff)
	}

	fetched, err := fake.FetchExperiment(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperiment() failed: %v", err)
	}
	if diff := cmp.Diff(exp, fetched, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperiment() mismatch (-want +got):\n%s", diff)
	}

	status, err := fake.FetchExperimentStatus(ctx, 100)
	if err != nil {
		t.Fatalf("FetchExperimentStatus() failed: %v", err)
	}
	if status.GetActiveState() != essmsgpb.ExperimentStatus_ACTIVE {
		t.Errorf("FetchExperimentStatus() got %v, want ACTIVE", status.GetActiveState())
	}
}

func TestFakeStorage_WorkUnitCRUD(t *testing.T) {
	ctx := context.Background()
	fake := storagetesting.NewFakeStorage()

	exp := &essmsgpb.Experiment{
		Name:  "experiments/200",
		Title: "Experiment 200",
	}
	if _, err := fake.CreateExperiment(ctx, exp); err != nil {
		t.Fatalf("CreateExperiment() failed: %v", err)
	}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/200/workUnits/0", // Zero WID for auto-allocation
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name: "experiments/200/workUnits/0/executions/1",
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RUNNING,
			},
		},
	}

	created, err := fake.CreateWorkUnit(ctx, wu)
	if err != nil {
		t.Fatalf("CreateWorkUnit() failed: %v", err)
	}
	if created.GetName() != "experiments/200/workUnits/1" {
		t.Errorf("CreateWorkUnit() name = %q, want experiments/200/workUnits/1", created.GetName())
	}

	fetched, err := fake.FetchWorkUnit(ctx, 200, 1)
	if err != nil {
		t.Fatalf("FetchWorkUnit() failed: %v", err)
	}
	if fetched.GetName() != "experiments/200/workUnits/1" {
		t.Errorf("FetchWorkUnit() name = %q, want experiments/200/workUnits/1", fetched.GetName())
	}
}

func TestFakeStorage_XidEntryCRUD(t *testing.T) {
	ctx := context.Background()
	fake := storagetesting.NewFakeStorage()

	entry := &xidmsgpb.XidEntry{
		Name: proto.String("xids/300"),
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{
				{
					Principal: &xidmsgpb.XidAcls_Role_User{
						User: "user@example.com",
					},
				},
			},
		},
	}

	created, err := fake.CreateXidEntry(ctx, entry)
	if err != nil {
		t.Fatalf("CreateXidEntry() failed: %v", err)
	}
	if diff := cmp.Diff(entry, created, protocmp.Transform()); diff != "" {
		t.Errorf("CreateXidEntry() mismatch (-want +got):\n%s", diff)
	}

	fetched, err := fake.FetchXidEntry(ctx, 300)
	if err != nil {
		t.Fatalf("FetchXidEntry() failed: %v", err)
	}
	if diff := cmp.Diff(entry, fetched, protocmp.Transform()); diff != "" {
		t.Errorf("FetchXidEntry() mismatch (-want +got):\n%s", diff)
	}
}

func TestFakeStorage_ArtifactCRUD(t *testing.T) {
	ctx := context.Background()
	fake := storagetesting.NewFakeStorage()

	art := &essmsgpb.Artifact{
		Parent: "experiments/100",
		Url:    &essmsgpb.Url{Url: "http://example.com/art.txt"},
	}

	created, err := fake.CreateArtifact(ctx, art)
	if err != nil {
		t.Fatalf("CreateArtifact() failed: %v", err)
	}
	if created.GetName() != "artifacts/1" {
		t.Errorf("CreateArtifact() name = %q, want artifacts/1", created.GetName())
	}

	fetched, err := fake.FetchArtifact(ctx, 1)
	if err != nil {
		t.Fatalf("FetchArtifact() failed: %v", err)
	}
	if fetched.GetUrl().GetUrl() != "http://example.com/art.txt" {
		t.Errorf("FetchArtifact() url = %q, want http://example.com/art.txt", fetched.GetUrl().GetUrl())
	}

	if err := fake.DeleteArtifact(ctx, 1); err != nil {
		t.Fatalf("DeleteArtifact() failed: %v", err)
	}
	if _, err := fake.FetchArtifact(ctx, 1); err == nil {
		t.Errorf("FetchArtifact() after delete returned nil error, want error")
	}
}

func TestFakeStorage_StatusMessage(t *testing.T) {
	ctx := context.Background()
	fake := storagetesting.NewFakeStorage()

	msg := &essmsgpb.StructuredMessage{
		UserFacingText: "Test status message",
		Severity:       essmsgpb.StructuredMessage_INFO,
	}

	fake.SaveStatusMessage("experiments/100", msg)

	list, _, err := fake.ListStatusMessages(ctx, "experiments/100", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages() failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListStatusMessages() len = %d, want 1", len(list))
	}
	if list[0].GetContent().GetUserFacingText() != "Test status message" {
		t.Errorf("ListStatusMessages() text = %q, want Test status message", list[0].GetContent().GetUserFacingText())
	}
}
