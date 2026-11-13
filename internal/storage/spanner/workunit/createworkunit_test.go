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

package workunit_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

// trialWorkUnitProto returns spanner.BlankWorkUnitProto(xid, wid) as a study trial.
func trialWorkUnitProto(xid, wid int64) *essmsgpb.WorkUnit {
	wu := spanner.BlankWorkUnitProto(xid, wid)
	wu.Parameters = experimenttest.TrialParameters()
	wu.TrialId = fmt.Sprintf("trial-%d", wid)
	return wu
}

// trialFields projects wu onto its name and study trial fields.
func trialFields(wu *essmsgpb.WorkUnit) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{Name: wu.GetName(), Parameters: wu.GetParameters(), TrialId: wu.GetTrialId()}
}

func TestTxRepository_InsertWorkUnits(t *testing.T) {
	ctx := t.Context()
	_, client := emulator.Setup(ctx, t)
	s, err := spanner.NewStorage(client)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}
	const xid = 123
	spanner.CreateBlankExperiment(ctx, t, s, xid)
	insert := func(wus []*essmsgpb.WorkUnit) error {
		return s.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
			return tx.WorkUnits.InsertWorkUnits(ctx, wus)
		})
	}

	want := []*essmsgpb.WorkUnit{trialWorkUnitProto(xid, 1), spanner.BlankWorkUnitProto(xid, 2), trialWorkUnitProto(xid, 3)}
	if err := insert(want); err != nil {
		t.Fatalf("InsertWorkUnits() failed: %v", err)
	}
	for _, wu := range want {
		_, wid, _ := validator.ValidateWorkUnitName(wu.GetName())
		got, err := s.FetchWorkUnit(ctx, xid, wid)
		if err != nil {
			t.Fatalf("FetchWorkUnit(%d) failed: %v", wid, err)
		}
		if diff := cmp.Diff(trialFields(wu), trialFields(got), protocmp.Transform()); diff != "" {
			t.Errorf("FetchWorkUnit(%d) mismatch (-want +got):\n%s", wid, diff)
		}
	}
	listed, _, err := s.ListWorkUnits(ctx, validator.ConstructExperimentName(xid), 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkUnits() failed: %v", err)
	}
	if len(listed) != len(want) {
		t.Fatalf("ListWorkUnits() returned %d work units, want %d", len(listed), len(want))
	}
	for i := range want {
		if diff := cmp.Diff(trialFields(want[i]), trialFields(listed[i]), protocmp.Transform()); diff != "" {
			t.Errorf("ListWorkUnits()[%d] mismatch (-want +got):\n%s", i, diff)
		}
	}

	tests := []struct {
		name     string
		wus      []*essmsgpb.WorkUnit
		wantCode codes.Code
	}{
		{name: "empty batch", wus: nil, wantCode: codes.OK},
		{name: "nil work unit", wus: []*essmsgpb.WorkUnit{trialWorkUnitProto(xid, 4), nil}, wantCode: codes.InvalidArgument},
		{name: "mixed experiments", wus: []*essmsgpb.WorkUnit{trialWorkUnitProto(xid, 4), trialWorkUnitProto(xid+1, 1)}, wantCode: codes.InvalidArgument},
		{name: "invalid execution name", wus: []*essmsgpb.WorkUnit{{Name: "invalid"}}, wantCode: codes.InvalidArgument},
		{name: "duplicate work unit rolls back the batch", wus: []*essmsgpb.WorkUnit{trialWorkUnitProto(xid, 4), trialWorkUnitProto(xid, 3)}, wantCode: codes.AlreadyExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := insert(tt.wus); status.Code(err) != tt.wantCode {
				t.Errorf("InsertWorkUnits() = %v (code %v), want code %v", err, status.Code(err), tt.wantCode)
			}
		})
	}
	if _, err := s.FetchWorkUnit(ctx, xid, 4); err == nil {
		t.Error("FetchWorkUnit(4) after failed batches succeeded, want error")
	}
}
