// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clisession_test

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage/spanner/clisession"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/auth/oauth2"
)

var (
	issuedAt  = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	expiresAt = issuedAt.Add(12 * time.Hour)
	revokedAt = issuedAt.Add(time.Hour)
)

// storedSession is a full CliSessions row.
type storedSession struct {
	Session   oauth2.CLISession
	RevokedAt spanner.NullTime
}

func newTestRepository(t *testing.T) (*clisession.Repository, *spanner.Client) {
	t.Helper()
	_, essClient := emulator.Setup(context.Background(), t)
	repo, err := clisession.NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() unexpected error: %v", err)
	}
	return repo, essClient
}

func baseSession(jti string) oauth2.CLISession {
	return oauth2.CLISession{JTI: jti, Email: "user@example.com", IssuedAt: issuedAt, ExpiresAt: expiresAt}
}

func createSession(t *testing.T, repo *clisession.Repository, jti string) {
	t.Helper()
	if err := repo.Create(context.Background(), baseSession(jti)); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
}

func readSession(t *testing.T, client *spanner.Client, jti string) storedSession {
	t.Helper()
	row, err := client.Single().ReadRow(context.Background(), "CliSessions", spanner.Key{jti},
		[]string{"Jti", "Email", "IssuedAt", "ExpiresAt", "RevokedAt"})
	if err != nil {
		t.Fatalf("ReadRow() unexpected error: %v", err)
	}
	var got storedSession
	if err := row.Columns(&got.Session.JTI, &got.Session.Email, &got.Session.IssuedAt, &got.Session.ExpiresAt, &got.RevokedAt); err != nil {
		t.Fatalf("row.Columns() unexpected error: %v", err)
	}
	return got
}

func TestNewRepository_NilClient(t *testing.T) {
	if _, err := clisession.NewRepository(nil); err == nil {
		t.Error("NewRepository(nil) error = nil, want non-nil")
	}
}

func TestRepository_Create(t *testing.T) {
	repo, client := newTestRepository(t)
	createSession(t, repo, "jti-create")
	want := storedSession{Session: baseSession("jti-create")}
	if diff := cmp.Diff(want, readSession(t, client, "jti-create")); diff != "" {
		t.Errorf("stored session mismatch (-want +got):\n%s", diff)
	}
	if err := repo.Create(context.Background(), baseSession("jti-create")); err == nil {
		t.Error("second Create() error = nil, want an already-exists error")
	}
}

func TestRepository_IsRevoked(t *testing.T) {
	tests := []struct {
		name   string
		create bool
		revoke bool
		want   bool
	}{
		{name: "active session", create: true, want: false},
		{name: "revoked session", create: true, revoke: true, want: true},
		{name: "unknown session counts as revoked", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo, client := newTestRepository(t)
			const jti = "jti-is-revoked"
			if tt.create {
				createSession(t, repo, jti)
			}
			if tt.revoke {
				revokeSession(t, client, jti)
			}
			got, err := repo.IsRevoked(ctx, jti)
			if err != nil {
				t.Fatalf("IsRevoked() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsRevoked() = %t, want %t", got, tt.want)
			}
		})
	}
}

// revokeSession sets RevokedAt directly, as an operator would.
func revokeSession(t *testing.T, client *spanner.Client, jti string) {
	t.Helper()
	mutation := spanner.Update("CliSessions", []string{"Jti", "RevokedAt"}, []any{jti, revokedAt})
	if _, err := client.Apply(context.Background(), []*spanner.Mutation{mutation}); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
}

func TestRepository_FailsOnSpannerErrors(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepository(t)
	client.Close()
	if err := repo.Create(ctx, baseSession("jti-closed")); err == nil {
		t.Error("Create() on a closed client error = nil, want non-nil")
	}
	if _, err := repo.IsRevoked(ctx, "jti-closed"); err == nil {
		t.Error("IsRevoked() on a closed client error = nil, want non-nil")
	}
}

func TestCliSessionsHasRowDeletionPolicy(t *testing.T) {
	_, client := newTestRepository(t)
	stmt := spanner.Statement{SQL: `SELECT ROW_DELETION_POLICY_EXPRESSION FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = '' AND TABLE_NAME = 'CliSessions'`}
	var policy spanner.NullString
	err := client.Single().Query(context.Background(), stmt).Do(func(row *spanner.Row) error {
		return row.Columns(&policy)
	})
	if err != nil {
		t.Fatalf("Query() unexpected error: %v", err)
	}
	if want := "OLDER_THAN(ExpiresAt, INTERVAL 1 DAY)"; policy.StringVal != want {
		t.Errorf("row deletion policy = %q, want %q", policy.StringVal, want)
	}
}
