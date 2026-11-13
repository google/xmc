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

package workunit

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/validator"
)

// newWorkUnit returns a work unit wid of experiment xid with its first execution named.
func newWorkUnit(xid, wid int64) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Name:                     validator.ConstructWorkUnitName(xid, wid),
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: validator.ConstructWorkUnitExecutionName(xid, wid, 1)},
	}
}

func TestInsertWorkUnit_NilWorkUnit(t *testing.T) {
	err := (&TxRepository{}).InsertWorkUnit(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("InsertWorkUnit(nil wu) = %v, want InvalidArgument", err)
	}
}

func TestInsertWorkUnits_RejectsInvalidBatches(t *testing.T) {
	tests := []struct {
		name string
		wus  []*essmsgpb.WorkUnit
	}{
		{name: "nil work unit", wus: []*essmsgpb.WorkUnit{newWorkUnit(1, 1), nil}},
		{name: "mixed experiments", wus: []*essmsgpb.WorkUnit{newWorkUnit(1, 1), newWorkUnit(2, 1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The transaction is nil: the batch is rejected before it is used.
			err := (&TxRepository{}).InsertWorkUnits(t.Context(), tt.wus)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("InsertWorkUnits() = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestInsertWorkUnit_NilTransaction(t *testing.T) {
	ctx := context.Background()
	r := &TxRepository{}

	wu := &essmsgpb.WorkUnit{
		Name: "experiments/1001/workUnits/1",
	}

	err := r.InsertWorkUnit(ctx, wu)
	if err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("InsertWorkUnit(nil tx) returned error: %v, want 'transaction must not be nil'", err)
	}
}
