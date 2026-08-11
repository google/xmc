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
)

func TestFetchActiveWorkUnitsByQueues(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name       string
		ctx        context.Context
		repo       *Repository
		queueNames []string
		pageSize   int32
		pageToken  string
		wantErr    bool
		wantCount  int
	}{
		{
			name:       "nil queue names with uninitialized client",
			ctx:        context.Background(),
			repo:       &Repository{},
			queueNames: nil,
			pageSize:   10,
			pageToken:  "",
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "empty queue names with uninitialized client",
			ctx:        context.Background(),
			repo:       &Repository{},
			queueNames: []string{},
			pageSize:   10,
			pageToken:  "",
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "non-empty queue names with nil client guard",
			ctx:        context.Background(),
			repo:       &Repository{client: nil},
			queueNames: []string{"gpu-queue"},
			pageSize:   10,
			pageToken:  "",
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "nil repository guard",
			ctx:        context.Background(),
			repo:       nil,
			queueNames: []string{"gpu-queue"},
			pageSize:   10,
			pageToken:  "",
			wantErr:    false,
			wantCount:  0,
		},
		{
			name:       "canceled context error",
			ctx:        canceledCtx,
			repo:       &Repository{},
			queueNames: []string{"gpu-queue"},
			pageSize:   10,
			pageToken:  "",
			wantErr:    true,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.repo.FetchActiveWorkUnitsByQueuesAt(tt.ctx, tt.queueNames, tt.pageSize, tt.pageToken)
			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchActiveWorkUnitsByQueuesAt() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if res == nil {
					t.Fatalf("FetchActiveWorkUnitsByQueuesAt() expected non-nil result")
				}
				if len(res.WorkUnits) != tt.wantCount {
					t.Errorf("FetchActiveWorkUnitsByQueuesAt() workUnits count = %d, want %d", len(res.WorkUnits), tt.wantCount)
				}
			}
		})
	}
}
