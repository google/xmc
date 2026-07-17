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
	"errors"
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

func TestFetchWorkUnitOperation(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storage, 123)
	wu := createBlankWorkUnit(ctx, t, storage, 123, 1)

	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		t.Fatalf("ValidateWorkUnitName failed: %v", err)
	}

	opMetadata := &essmsgpb.OperationMetadata{
		ActionType: essmsgpb.OperationMetadata_CREATE,
		Target:     wu.GetName(),
	}

	addedOp, err := storage.AddWorkUnitOperation(ctx, xid, wid, opMetadata)
	if err != nil {
		t.Fatalf("AddWorkUnitOperation failed: %v", err)
	}

	xid, wid, oid, err := validator.ValidateOperationName(addedOp.GetName())
	if err != nil {
		t.Fatalf("ValidateOperationName failed: %v", err)
	}

	gotOp, err := storage.FetchWorkUnitOperation(ctx, xid, wid, oid)
	if err != nil {
		t.Fatalf("FetchWorkUnitOperation failed: %v", err)
	}

	if diff := cmp.Diff(addedOp, gotOp, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnitOperation(%d, %d, %s) returned diff (-want +got):\n%s", xid, wid, oid, diff)
	}
}

func TestFetchWorkUnitOperation_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	_, err = storage.FetchWorkUnitOperation(ctx, 123, 1, "non-existent-operation")
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("FetchWorkUnitOperation for non-existent operation returned error %v, want %v", err, status.ErrNotFound)
	}
}
