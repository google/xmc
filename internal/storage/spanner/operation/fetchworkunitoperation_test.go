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
	"errors"
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

func TestFetchWorkUnitOperation(t *testing.T) {
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
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	_, err = storage.FetchWorkUnitOperation(ctx, 123, 1, "non-existent-operation")
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchWorkUnitOperation for non-existent operation returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}
