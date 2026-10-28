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

package clilogincode

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/auth/oauth2"
)

func newTestRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	_, essPool := postgrestest.Setup(t.Context(), t)
	return NewRepository(essPool), essPool
}

func baseCode(seed string) oauth2.CLILoginCode {
	hash := sha256.Sum256([]byte(seed))
	return oauth2.CLILoginCode{
		Hash:          hash[:],
		Email:         "user@example.com",
		Name:          "Example User",
		CodeChallenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
		ExpiresAt:     time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
	}
}

// readCode reads the stored row for hash directly from PostgreSQL and reports whether Name is SQL NULL.
func readCode(ctx context.Context, t *testing.T, pool *pgxpool.Pool, hash []byte) (oauth2.CLILoginCode, bool) {
	t.Helper()
	const query = `SELECT CodeHash, Email, Name, CodeChallenge, ExpiresAt FROM CliLoginCodes WHERE CodeHash = $1`
	var (
		code oauth2.CLILoginCode
		name pgtype.Text
	)
	if err := pool.QueryRow(ctx, query, hash).Scan(&code.Hash, &code.Email, &name, &code.CodeChallenge, &code.ExpiresAt); err != nil {
		t.Fatalf("QueryRow() unexpected error: %v", err)
	}
	code.Name = name.String
	code.ExpiresAt = code.ExpiresAt.UTC()
	return code, !name.Valid
}

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}
}

func TestRepository_Save(t *testing.T) {
	noName := baseCode("no-name")
	noName.Name = ""
	tests := []struct {
		name         string
		code         oauth2.CLILoginCode
		wantNullName bool
	}{
		{name: "all fields", code: baseCode("all-fields"), wantNullName: false},
		{name: "null name", code: noName, wantNullName: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repo, pool := newTestRepository(t)
			if err := repo.Save(ctx, tt.code); err != nil {
				t.Fatalf("Save() unexpected error: %v", err)
			}
			gotStored, isNullName := readCode(ctx, t, pool, tt.code.Hash)
			if diff := cmp.Diff(tt.code, gotStored); diff != "" {
				t.Errorf("stored code mismatch (-want +got):\n%s", diff)
			}
			if isNullName != tt.wantNullName {
				t.Errorf("stored Name IS NULL = %v, want %v", isNullName, tt.wantNullName)
			}
			gotConsumed, err := repo.Consume(ctx, tt.code.Hash)
			if err != nil {
				t.Fatalf("Consume() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.code, gotConsumed); diff != "" {
				t.Errorf("consumed code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepository_SaveDuplicate(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	code := baseCode("duplicate")
	if err := repo.Save(ctx, code); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}
	err := repo.Save(ctx, code)
	if !errors.Is(err, grpcerr.ErrAlreadyExists) || status.Code(err) != codes.AlreadyExists {
		t.Errorf("second Save() error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrAlreadyExists, codes.AlreadyExists)
	}
}

func TestRepository_ConsumeDeletesTheCode(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	code := baseCode("consume")
	if err := repo.Save(ctx, code); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}
	got, err := repo.Consume(ctx, code.Hash)
	if err != nil {
		t.Fatalf("Consume() unexpected error: %v", err)
	}
	if diff := cmp.Diff(code, got); diff != "" {
		t.Errorf("Consume() mismatch (-want +got):\n%s", diff)
	}
	if _, err := repo.Consume(ctx, code.Hash); !errors.Is(err, oauth2.ErrCLICodeNotFound) {
		t.Errorf("second Consume() error = %v, want %v", err, oauth2.ErrCLICodeNotFound)
	}
}

func TestRepository_ConsumeQueryErrorIsNotMaskedAsNotFound(t *testing.T) {
	repo, _ := newTestRepository(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := repo.Consume(ctx, []byte("hash")); !errors.Is(err, context.Canceled) {
		t.Errorf("Consume(canceled ctx) error = %v, want %v", err, context.Canceled)
	}
}

func TestRepository_ConcurrentConsumeSucceedsOnce(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)
	code := baseCode("concurrent")
	if err := repo.Save(ctx, code); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	const attempts = 5
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			_, errs[i] = repo.Consume(ctx, code.Hash)
		})
	}
	wg.Wait()

	var succeeded int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, oauth2.ErrCLICodeNotFound):
			t.Errorf("Consume() unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("successful Consume() calls = %d, want 1", succeeded)
	}
}
