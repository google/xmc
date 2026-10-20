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

package clilogincode_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage/spanner/clilogincode"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/auth/oauth2"
)

func newTestRepository(t *testing.T) (*clilogincode.Repository, *spanner.Client) {
	t.Helper()
	_, essClient := emulator.Setup(context.Background(), t)
	repo, err := clilogincode.NewRepository(essClient)
	if err != nil {
		t.Fatalf("NewRepository() unexpected error: %v", err)
	}
	return repo, essClient
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

// readCode reads the stored row for hash directly from Spanner.
func readCode(t *testing.T, client *spanner.Client, hash []byte) oauth2.CLILoginCode {
	t.Helper()
	row, err := client.Single().ReadRow(context.Background(), "CliLoginCodes", spanner.Key{hash},
		[]string{"CodeHash", "Email", "Name", "CodeChallenge", "ExpiresAt"})
	if err != nil {
		t.Fatalf("ReadRow() unexpected error: %v", err)
	}
	var code oauth2.CLILoginCode
	var name spanner.NullString
	if err := row.Columns(&code.Hash, &code.Email, &name, &code.CodeChallenge, &code.ExpiresAt); err != nil {
		t.Fatalf("row.Columns() unexpected error: %v", err)
	}
	code.Name = name.StringVal
	return code
}

func TestNewRepository_NilClient(t *testing.T) {
	if _, err := clilogincode.NewRepository(nil); err == nil {
		t.Error("NewRepository(nil) error = nil, want non-nil")
	}
}

func TestRepository_Save(t *testing.T) {
	noName := baseCode("no-name")
	noName.Name = ""
	tests := []struct {
		name string
		code oauth2.CLILoginCode
	}{
		{name: "all fields", code: baseCode("all-fields")},
		{name: "null name", code: noName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, client := newTestRepository(t)
			if err := repo.Save(context.Background(), tt.code); err != nil {
				t.Fatalf("Save() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.code, readCode(t, client, tt.code.Hash)); diff != "" {
				t.Errorf("stored code mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRepository_SaveDuplicate(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestRepository(t)
	code := baseCode("duplicate")
	if err := repo.Save(ctx, code); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}
	if err := repo.Save(ctx, code); err == nil {
		t.Error("second Save() error = nil, want an already-exists error")
	}
}
