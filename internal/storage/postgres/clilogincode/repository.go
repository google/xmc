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

// Package clilogincode provides the PostgreSQL store for one-time CLI login codes.
package clilogincode

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/auth/oauth2"
)

var _ storage.CLILoginCodeRepository = (*Repository)(nil)

// Repository implements storage.CLILoginCodeRepository for PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// NewRepository creates a new PostgreSQL CLI login code Repository bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// Save inserts a new CLI login authorization code.
func (r *Repository) Save(ctx context.Context, code oauth2.CLILoginCode) error {
	return status.Error(codes.Unimplemented, "postgres clilogincode repository: Save is not implemented")
}
