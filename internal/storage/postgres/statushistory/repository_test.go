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

package statushistory

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
			name: "FetchBatchQueueVelocity",
			call: func() error {
				_, err := repo.FetchBatchQueueVelocity(ctx, []string{"q1"}, time.Hour)
				return err
			},
		},
		{
			name: "FetchBatchPendingQueueMetrics",
			call: func() error {
				_, err := repo.FetchBatchPendingQueueMetrics(ctx, []string{"q1"})
				return err
			},
		},
		{
			name: "Tx_SaveWorkUnitStatusHistory",
			call: func() error {
				return txRepo.SaveWorkUnitStatusHistory(ctx, 1, 1, nil, "q1", nil, nil, nil, time.Time{})
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
