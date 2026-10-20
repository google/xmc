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

package postgres

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
)

func TestStorage(t *testing.T) {
	ctx := context.Background()
	s := NewStorage()
	if s == nil {
		t.Fatal("NewStorage() returned nil")
	}

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "PauseState",
			check: func(t *testing.T) {
				if s.pauseStateRepository == nil {
					t.Error("pauseStateRepository was not initialized")
				}
				if s.PauseState() != s.pauseStateRepository {
					t.Error("PauseState() did not return pauseStateRepository")
				}
			},
		},
		{
			name: "PausedTasks",
			check: func(t *testing.T) {
				if s.pausedTaskRepository == nil {
					t.Error("pausedTaskRepository was not initialized")
				}
				if s.PausedTasks() != s.pausedTaskRepository {
					t.Error("PausedTasks() did not return pausedTaskRepository")
				}
			},
		},
		{
			name: "RunInTransaction",
			check: func(t *testing.T) {
				err := s.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
					return nil
				})
				if got := status.Code(err); got != codes.Unimplemented {
					t.Errorf("RunInTransaction() code = %v, want %v (err: %v)", got, codes.Unimplemented, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t)
		})
	}
}
