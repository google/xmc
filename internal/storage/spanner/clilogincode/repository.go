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
	"google.golang.org/grpc/codes"
	"xmanager_cloud/pkg/auth/oauth2"
)

var _ oauth2.CLICodeStore = (*Repository)(nil)

// Spanner table and columns of the CliLoginCodes table.
const (
	tableName        = "CliLoginCodes"
	colCodeHash      = "CodeHash"
	colEmail         = "Email"
	colName          = "Name"
	colCodeChallenge = "CodeChallenge"
	colExpiresAt     = "ExpiresAt"
)

var columns = []string{colCodeHash, colEmail, colName, colCodeChallenge, colExpiresAt}

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
	mutation := spanner.Insert(tableName, columns, []any{
		code.Hash,
		code.Email,
		spanner.NullString{StringVal: code.Name, Valid: code.Name != ""},
		code.CodeChallenge,
		code.ExpiresAt,
	})
	if _, err := r.client.Apply(ctx, []*spanner.Mutation{mutation}); err != nil {
		return fmt.Errorf("failed to save cli login code: %w", err)
	}
	return nil
}

// Consume reads and deletes the code with the given hash in one read-write transaction, so
// concurrent redemptions of the same code succeed at most once.
func (r *Repository) Consume(ctx context.Context, hash []byte) (oauth2.CLILoginCode, error) {
	var code oauth2.CLILoginCode
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		row, err := tx.ReadRow(ctx, tableName, spanner.Key{hash}, columns)
		if spanner.ErrCode(err) == codes.NotFound {
			return oauth2.ErrCLICodeNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to read cli login code: %w", err)
		}
		if code, err = rowToCode(row); err != nil {
			return err
		}
		return tx.BufferWrite([]*spanner.Mutation{spanner.Delete(tableName, spanner.Key{hash})})
	})
	if err != nil {
		return oauth2.CLILoginCode{}, err
	}
	return code, nil
}

func rowToCode(row *spanner.Row) (oauth2.CLILoginCode, error) {
	var code oauth2.CLILoginCode
	var name spanner.NullString
	if err := row.Columns(&code.Hash, &code.Email, &name, &code.CodeChallenge, &code.ExpiresAt); err != nil {
		return oauth2.CLILoginCode{}, fmt.Errorf("failed to decode cli login code: %w", err)
	}
	code.Name = name.StringVal
	return code, nil
}
