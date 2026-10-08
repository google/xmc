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
	"testing"

	cloudspanner "cloud.google.com/go/spanner"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestFetchNextWorkUnitIDTransaction(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		setup      func(context.Context, *testing.T, *spanner.Storage)
		xid        int64
		wantNextID int64
		wantErr    bool
	}{
		{
			name: "No work units",
			setup: func(ctx context.Context, t *testing.T, s *spanner.Storage) {
				spanner.CreateBlankExperiment(ctx, t, s, 123)
			},
			xid:        123,
			wantNextID: 1,
		},
		{
			name: "One work unit with ID 1",
			setup: func(ctx context.Context, t *testing.T, s *spanner.Storage) {
				spanner.CreateBlankExperiment(ctx, t, s, 123)
				spanner.CreateBlankWorkUnit(ctx, t, s, 123, 1)
			},
			xid:        123,
			wantNextID: 2,
		},
		{
			name: "Work unit with ID 10",
			setup: func(ctx context.Context, t *testing.T, s *spanner.Storage) {
				spanner.CreateBlankExperiment(ctx, t, s, 123)
				spanner.CreateBlankWorkUnit(ctx, t, s, 123, 10)
			},
			xid:        123,
			wantNextID: 11,
		},
		{
			name: "Different XID",
			setup: func(ctx context.Context, t *testing.T, s *spanner.Storage) {
				spanner.CreateBlankExperiment(ctx, t, s, 123)
				spanner.CreateBlankWorkUnit(ctx, t, s, 123, 5)
				spanner.CreateBlankExperiment(ctx, t, s, 456)
			},
			xid:        456,
			wantNextID: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client := emulator.Setup(ctx, t)
			storage, err := spanner.NewStorage(client)
			if err != nil {
				t.Fatalf("spanner.NewStorage failed: %v", err)
			}

			if tc.setup != nil {
				tc.setup(ctx, t, storage)
			}

			var nextID int64
			_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *cloudspanner.ReadWriteTransaction) error {
				var err error
				nextID, err = storage.FetchNextWorkUnitIDTransaction(ctx, tc.xid, txn)
				return err
			})

			if (err != nil) != tc.wantErr {
				t.Errorf("fetchNextWorkUnitIDTransaction() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if nextID != tc.wantNextID {
				t.Errorf("fetchNextWorkUnitIDTransaction() = %v, want %v", nextID, tc.wantNextID)
			}
		})
	}
}
