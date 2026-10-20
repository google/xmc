// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
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
	"xmanager_cloud/internal/storage"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository()
	if repo == nil {
		t.Fatal("NewRepository() returned nil")
	}
	txRepo := repo.WithTx()
	if txRepo == nil {
		t.Fatal("WithTx() returned nil")
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "FetchWorkUnit",
			call: func() error {
				_, err := repo.FetchWorkUnit(ctx, 1, 1)
				return err
			},
		},
		{
			name: "FetchCurrentWorkUnitExecution",
			call: func() error {
				_, err := repo.FetchCurrentWorkUnitExecution(ctx, 1, 1)
				return err
			},
		},
		{
			name: "ListWorkUnitExecutions",
			call: func() error {
				_, err := repo.ListWorkUnitExecutions(ctx, 1, 1)
				return err
			},
		},
		{
			name: "UpdateWorkUnit",
			call: func() error {
				_, err := repo.UpdateWorkUnit(ctx, &storage.WorkUnitUpdate{})
				return err
			},
		},
		{
			name: "ListWorkUnits",
			call: func() error {
				_, _, err := repo.ListWorkUnits(ctx, "experiments/1", 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "FetchActiveWorkUnitsByQueues",
			call: func() error {
				_, err := repo.FetchActiveWorkUnitsByQueues(ctx, []string{"q1"}, 10, "")
				return err
			},
		},
		{
			name: "Tx_FetchWorkUnit",
			call: func() error {
				_, err := txRepo.FetchWorkUnit(ctx, 1, 1)
				return err
			},
		},
		{
			name: "Tx_FetchUpdatingWorkUnits",
			call: func() error {
				_, err := txRepo.FetchUpdatingWorkUnits(ctx, 1, []int64{1})
				return err
			},
		},
		{
			name: "Tx_SaveWorkUnitExecution",
			call: func() error {
				return txRepo.SaveWorkUnitExecution(ctx, nil)
			},
		},
		{
			name: "Tx_UpdateCurrentWorkUnitExecution",
			call: func() error {
				return txRepo.UpdateCurrentWorkUnitExecution(ctx, 1, 1, 1, nil)
			},
		},
		{
			name: "Tx_ListWorkUnitMessagesBySeverity",
			call: func() error {
				_, err := txRepo.ListWorkUnitMessagesBySeverity(ctx, 1, essmsgpb.StructuredMessage_ERROR)
				return err
			},
		},
		{
			name: "Tx_FetchNextWorkUnitID",
			call: func() error {
				_, err := txRepo.FetchNextWorkUnitID(ctx, 1)
				return err
			},
		},
		{
			name: "Tx_InsertWorkUnit",
			call: func() error {
				return txRepo.InsertWorkUnit(ctx, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s code = %v, want %v (err: %v)", tt.name, got, codes.Unimplemented, err)
			}
		})
	}
}
