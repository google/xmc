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

// Package clisession provides the Cloud Spanner store for CLI sessions.
package clisession

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/pkg/auth/oauth2"
)

var _ oauth2.CLISessionStore = (*Repository)(nil)

// Spanner table and columns of the CliSessions table.
const (
	tableName    = "CliSessions"
	colJti       = "Jti"
	colEmail     = "Email"
	colIssuedAt  = "IssuedAt"
	colExpiresAt = "ExpiresAt"
)

// Repository stores CLI sessions in Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// NewRepository creates a Spanner-backed CLI session Repository.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, errors.New("spanner client must not be nil")
	}
	return &Repository{client: client}, nil
}

// Create inserts a new session. Inserting a jti that already exists fails.
func (r *Repository) Create(ctx context.Context, session oauth2.CLISession) error {
	mutation := spanner.Insert(tableName,
		[]string{colJti, colEmail, colIssuedAt, colExpiresAt},
		[]any{session.JTI, session.Email, session.IssuedAt, session.ExpiresAt})
	if _, err := r.client.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return fmt.Errorf("failed to create cli session: %w", err)
	}
	return nil
}
