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

package artifact

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
)

func TestRepository_Unimplemented(t *testing.T) {
	repo := NewRepository(nil)
	if repo == nil {
		t.Fatal("NewRepository() returned nil")
	}

	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "CreateArtifact",
			call: func() error {
				_, err := repo.CreateArtifact(ctx, nil)
				return err
			},
		},
		{
			name: "FetchArtifact",
			call: func() error {
				_, err := repo.FetchArtifact(ctx, 1)
				return err
			},
		},
		{
			name: "ListArtifacts",
			call: func() error {
				_, _, err := repo.ListArtifacts(ctx, "experiments/1", 10, "", "", "", 0)
				return err
			},
		},
		{
			name: "UpdateArtifact",
			call: func() error {
				_, err := repo.UpdateArtifact(ctx, &storage.ArtifactUpdate{})
				return err
			},
		},
		{
			name: "DeleteArtifact",
			call: func() error {
				return repo.DeleteArtifact(ctx, 1)
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

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}
}
