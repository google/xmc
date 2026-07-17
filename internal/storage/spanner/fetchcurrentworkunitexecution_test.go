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

func TestFetchCurrentWorkUnitExecution(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storage, 123)

	createdWu := createBlankWorkUnit(ctx, t, storage, 123, 1)

	xid, wid, err := validator.ValidateWorkUnitName(createdWu.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID, WID from name %q: %v", createdWu.GetName(), err)
	}

	gotWue, err := storage.FetchCurrentWorkUnitExecution(ctx, xid, wid)
	if err != nil {
		t.Fatalf("FetchCurrentWorkUnitExecution failed: %v", err)
	}

	wantWue := createdWu.CurrentWorkUnitExecution
	wantWue.State = essmsgpb.WorkUnitExecution_ACTIVE
	if diff := cmp.Diff(wantWue, gotWue, protocmp.Transform()); diff != "" {
		t.Errorf("FetchCurrentWorkUnitExecution(%d, %d) returned diff (-want +got):\n%s", xid, wid, diff)
	}
}

func TestFetchCurrentWorkUnitExecution_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	_, err = storage.FetchCurrentWorkUnitExecution(ctx, 123456789, 1)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("FetchCurrentWorkUnitExecution for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}

func TestFetchCurrentWorkUnitExecution_ExperimentExists_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	createBlankExperiment(ctx, t, storage, 123)

	_, err = storage.FetchCurrentWorkUnitExecution(ctx, 123, 999)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("FetchCurrentWorkUnitExecution for non-existent WorkUnit returned error %v, want %v", err, status.ErrNotFound)
	}
}
