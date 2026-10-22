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

package health

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRow is a pgx.Row that scans a fixed schema_migrations row, or fails with err.
type fakeRow struct {
	version int64
	dirty   bool
	err     error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.version
	*dest[1].(*bool) = r.dirty
	return nil
}

// fakeRowQuerier answers every query with row.
type fakeRowQuerier struct {
	row fakeRow
}

func (q fakeRowQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return q.row
}

func TestPostgresHealthClient_QuerySchemaVersion(t *testing.T) {
	ctx := context.Background()
	queryErr := errors.New("connection refused")

	tests := []struct {
		name        string
		row         fakeRow
		wantVersion int64
		wantDirty   bool
		wantErr     bool
		wantErrIs   error
	}{
		{
			name:        "clean schema",
			row:         fakeRow{version: 7},
			wantVersion: 7,
		},
		{
			name:        "dirty schema",
			row:         fakeRow{version: 7, dirty: true},
			wantVersion: 7,
			wantDirty:   true,
		},
		{
			name:    "no migration records",
			row:     fakeRow{err: pgx.ErrNoRows},
			wantErr: true,
		},
		{
			name:      "query error is wrapped",
			row:       fakeRow{err: queryErr},
			wantErr:   true,
			wantErrIs: queryErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &postgresHealthClient{db: fakeRowQuerier{row: tc.row}}

			gotVersion, gotDirty, err := client.QuerySchemaVersion(ctx)
			if (err != nil) != tc.wantErr {
				t.Fatalf("QuerySchemaVersion() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("QuerySchemaVersion() error = %v, want it to wrap %v", err, tc.wantErrIs)
			}
			if gotVersion != tc.wantVersion || gotDirty != tc.wantDirty {
				t.Errorf("QuerySchemaVersion() = (%d, %t), want (%d, %t)", gotVersion, gotDirty, tc.wantVersion, tc.wantDirty)
			}
		})
	}
}

func TestNewPostgresHealthClient_NilPool(t *testing.T) {
	_, _, err := NewPostgresHealthClient(nil).QuerySchemaVersion(context.Background())
	if !errors.Is(err, ErrNilClient) {
		t.Errorf("QuerySchemaVersion() with a nil pool error = %v, want %v", err, ErrNilClient)
	}
}
