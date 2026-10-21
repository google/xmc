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

// Package clilogincode provides the Cloud Spanner store for one-time CLI login codes.
package clilogincode

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/pkg/auth/oauth2"
)

var _ oauth2.CLICodeStore = (*Repository)(nil)

// Repository stores one-time CLI login codes in Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// NewRepository creates a Spanner-backed CLI login code Repository.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, errors.New("spanner client must not be nil")
	}
	return &Repository{client: client}, nil
}

// Save inserts a new code. Inserting a hash that already exists fails.
func (r *Repository) Save(ctx context.Context, code oauth2.CLILoginCode) error {
	cols := []string{"CodeHash", "Email", "Name", "CodeChallenge", "ExpiresAt"}
	vals := []any{code.Hash, code.Email, spanner.NullString{StringVal: code.Name, Valid: code.Name != ""}, code.CodeChallenge, code.ExpiresAt}
	if _, err := r.client.Apply(ctx, []*spanner.Mutation{spanner.Insert("CliLoginCodes", cols, vals)}); err != nil {
		return fmt.Errorf("failed to save cli login code: %w", err)
	}
	return nil
}
