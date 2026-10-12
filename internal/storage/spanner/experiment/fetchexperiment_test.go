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
package experiment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	"github.com/google/xmc/internal/grpcerr"
	"github.com/google/xmc/internal/storage/spanner"
	"github.com/google/xmc/internal/storage/spanner/emulator"
	"github.com/google/xmc/internal/validator"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestFetchExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, 123)

	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("Could not parse XID from name %q: %v", createdExp.GetName(), err)
	}

	gotExp, err := storage.FetchExperiment(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	if diff := cmp.Diff(createdExp, gotExp, protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.Experiment{}, "acls")); diff != "" {
		t.Errorf("FetchExperiment(%d) returned diff (-want +got):\n%s", xid, diff)
	}
}

func TestFetchExperiment_NotFound(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	_, err := storage.FetchExperiment(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) {
		t.Errorf("FetchExperiment for non-existent XID returned error %v, want %v", err, grpcerr.ErrNotFound)
	}
}
