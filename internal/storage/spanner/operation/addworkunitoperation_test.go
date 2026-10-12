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
package operation_test

import (
	"context"
	"strings"
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

func TestAddWorkUnitOperation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storage, 123)
	wu := spanner.CreateBlankWorkUnit(ctx, t, storage, 123, 1)

	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		t.Fatalf("ValidateWorkUnitName failed: %v", err)
	}

	opMetadata := &essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_CREATE,
		Target:     wu.GetName(),
	}

	op, err := storage.AddWorkUnitOperation(ctx, xid, wid, opMetadata)
	if err != nil {
		t.Fatalf("AddWorkUnitOperation failed: %v", err)
	}

	if op == nil {
		t.Fatalf("AddWorkUnitOperation returned nil operation")
	}

	if op.GetDone() {
		t.Errorf("AddWorkUnitOperation returned done operation, want not done")
	}

	if !strings.HasPrefix(op.GetName(), "experiments/123/workUnits/1/operations/") {
		t.Errorf("AddWorkUnitOperation returned operation with wrong name prefix: got %q, want prefix %q", op.GetName(), "experiments/123/workUnits/1/operations/")
	}

	gotMetadata := &essmsgpb.OperationMetadata{}
	if err := op.GetMetadata().UnmarshalTo(gotMetadata); err != nil {
		t.Fatalf("Failed to unmarshal operation metadata: %v", err)
	}

	if gotMetadata.GetActionType() != opMetadata.GetActionType() {
		t.Errorf("AddWorkUnitOperation returned operation with wrong action type: got %v, want %v", gotMetadata.GetActionType(), opMetadata.GetActionType())
	}

	if gotMetadata.GetTarget() != opMetadata.GetTarget() {
		t.Errorf("AddWorkUnitOperation returned operation with wrong target: got %q, want %q", gotMetadata.GetTarget(), opMetadata.GetTarget())
	}

	if gotMetadata.GetCreateTime() == nil {
		t.Errorf("AddWorkUnitOperation returned operation with nil CreateTime in metadata")
	}
}
