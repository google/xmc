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
package workunit_test

import (
	"context"
	"errors"
	"testing"
	"xmanager_cloud/internal/storage/spanner"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

func TestFetchWorkUnit(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storage, 123)

	createdWu := spanner.CreateBlankWorkUnit(ctx, t, storage, 123, 1)
	createdWu.CurrentWorkUnitExecution.State = essmsgpb.WorkUnitExecution_ACTIVE

	xid, wid, err := validator.ValidateWorkUnitName(createdWu.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID, WID from name %q: %v", createdWu.GetName(), err)
	}

	gotWu, err := storage.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchWorkUnit failed: %v", err)
	}

	if diff := cmp.Diff(createdWu, gotWu, protocmp.Transform()); diff != "" {
		t.Errorf("FetchWorkUnit(%d, %d) returned diff (-want +got):\n%s", xid, wid, diff)
	}
}

func TestFetchWorkUnit_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	_, err = storage.FetchWorkUnit(ctx, 123456789, 1)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchWorkUnit for non-existent XID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}

func TestFetchWorkUnit_ExperimentExists_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage failed: %v", err)
	}

	spanner.CreateBlankExperiment(ctx, t, storage, 123)

	_, err = storage.FetchWorkUnit(ctx, 123, 999)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchWorkUnit for non-existent WorkUnit returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}
