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
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

func TestFetchExperimentStatus(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := NewStorage(essClient)

	createdExp := createBlankExperiment(ctx, t, storage, 123)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdExp.GetName(), err)
	}

	gotStatus, err := storage.FetchExperimentStatus(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperimentStatus failed: %v", err)
	}

	if diff := cmp.Diff(createdExp.Status, gotStatus, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperimentStatus(%d) returned diff (-want +got):\n%s", xid, diff)
	}
}

func TestFetchExperimentStatus_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := NewStorage(essClient)

	_, err := storage.FetchExperimentStatus(ctx, 123456789)
	if !errors.Is(err, status.ErrNotFound) {
		t.Errorf("FetchExperimentStatus for non-existent XID returned error %v, want %v", err, status.ErrNotFound)
	}
}
