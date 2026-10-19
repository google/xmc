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

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

func TestTranslateError(t *testing.T) {
	genericErr := errors.New("connection reset by peer")
	uniquePgErr := &pgconn.PgError{Code: SQLStateUniqueViolation, Message: "duplicate key value"}
	fkPgErr := &pgconn.PgError{Code: SQLStateForeignKeyViolation, Message: "insert or update violates foreign key"}
	checkPgErr := &pgconn.PgError{Code: SQLStateCheckViolation, Message: "violates check constraint"}
	invalidTextPgErr := &pgconn.PgError{Code: SQLStateInvalidTextRepresentation, Message: "invalid input syntax"}
	serializationPgErr := &pgconn.PgError{Code: SQLStateSerializationFailure, Message: "could not serialize access"}
	deadlockPgErr := &pgconn.PgError{Code: SQLStateDeadlockDetected, Message: "deadlock detected"}
	unmappedPgErr := &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}

	tests := []struct {
		name         string
		err          error
		entity       string
		wantNil      bool
		wantCode     codes.Code
		wantSentinel error
		wantCause    error
		wantSubstr   string
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			entity:  "experiment",
			wantNil: true,
		},
		{
			name:         "pgx.ErrNoRows maps to NotFound",
			err:          pgx.ErrNoRows,
			entity:       "experiment",
			wantCode:     codes.NotFound,
			wantSentinel: grpcerr.ErrNotFound,
			wantCause:    pgx.ErrNoRows,
			wantSubstr:   "experiment not found",
		},
		{
			name:         "wrapped pgx.ErrNoRows maps to NotFound",
			err:          fmt.Errorf("query experiment: %w", pgx.ErrNoRows),
			entity:       "experiment",
			wantCode:     codes.NotFound,
			wantSentinel: grpcerr.ErrNotFound,
			wantCause:    pgx.ErrNoRows,
			wantSubstr:   "experiment not found",
		},
		{
			name:         "unique_violation 23505 maps to AlreadyExists",
			err:          uniquePgErr,
			entity:       "artifact",
			wantCode:     codes.AlreadyExists,
			wantSentinel: grpcerr.ErrAlreadyExists,
			wantCause:    uniquePgErr,
			wantSubstr:   "artifact already exists",
		},
		{
			name:         "wrapped unique_violation 23505 maps to AlreadyExists",
			err:          fmt.Errorf("insert artifact: %w", uniquePgErr),
			entity:       "artifact",
			wantCode:     codes.AlreadyExists,
			wantSentinel: grpcerr.ErrAlreadyExists,
			wantCause:    uniquePgErr,
			wantSubstr:   "artifact already exists",
		},
		{
			name:         "foreign_key_violation 23503 maps to FailedPrecondition",
			err:          fkPgErr,
			entity:       "work unit",
			wantCode:     codes.FailedPrecondition,
			wantSentinel: grpcerr.ErrFailedPrecondition,
			wantCause:    fkPgErr,
			wantSubstr:   "work unit foreign key violation",
		},
		{
			name:         "wrapped foreign_key_violation 23503 maps to FailedPrecondition",
			err:          fmt.Errorf("insert work unit: %w", fkPgErr),
			entity:       "work unit",
			wantCode:     codes.FailedPrecondition,
			wantSentinel: grpcerr.ErrFailedPrecondition,
			wantCause:    fkPgErr,
			wantSubstr:   "work unit foreign key violation",
		},
		{
			name:         "check_violation 23514 maps to InvalidArgument",
			err:          checkPgErr,
			entity:       "paused task",
			wantCode:     codes.InvalidArgument,
			wantSentinel: grpcerr.ErrInvalidArgument,
			wantCause:    checkPgErr,
			wantSubstr:   "invalid paused task",
		},
		{
			name:         "invalid_text_representation 22P02 maps to InvalidArgument",
			err:          invalidTextPgErr,
			entity:       "operation",
			wantCode:     codes.InvalidArgument,
			wantSentinel: grpcerr.ErrInvalidArgument,
			wantCause:    invalidTextPgErr,
			wantSubstr:   "invalid operation",
		},
		{
			name:         "serialization_failure 40001 maps to Aborted",
			err:          serializationPgErr,
			entity:       "experiment",
			wantCode:     codes.Aborted,
			wantSentinel: grpcerr.ErrAborted,
			wantCause:    serializationPgErr,
			wantSubstr:   "experiment transaction aborted",
		},
		{
			name:         "deadlock_detected 40P01 maps to Aborted",
			err:          deadlockPgErr,
			entity:       "work unit",
			wantCode:     codes.Aborted,
			wantSentinel: grpcerr.ErrAborted,
			wantCause:    deadlockPgErr,
			wantSubstr:   "work unit transaction aborted",
		},
		{
			name:       "unmapped PgError preserves cause with Unknown code",
			err:        unmappedPgErr,
			entity:     "experiment",
			wantCode:   codes.Unknown,
			wantCause:  unmappedPgErr,
			wantSubstr: "experiment storage error",
		},
		{
			name:       "generic error preserves cause with Unknown code",
			err:        genericErr,
			entity:     "xid",
			wantCode:   codes.Unknown,
			wantCause:  genericErr,
			wantSubstr: "xid storage error",
		},
		{
			name:         "empty entity name falls back to default entity label",
			err:          pgx.ErrNoRows,
			entity:       "   ",
			wantCode:     codes.NotFound,
			wantSentinel: grpcerr.ErrNotFound,
			wantCause:    pgx.ErrNoRows,
			wantSubstr:   "entity not found",
		},
		{
			name:       "context.Canceled passes through unchanged",
			err:        context.Canceled,
			entity:     "experiment",
			wantCode:   codes.Unknown,
			wantCause:  context.Canceled,
			wantSubstr: context.Canceled.Error(),
		},
		{
			name:       "context.DeadlineExceeded passes through unchanged",
			err:        context.DeadlineExceeded,
			entity:     "experiment",
			wantCode:   codes.Unknown,
			wantCause:  context.DeadlineExceeded,
			wantSubstr: context.DeadlineExceeded.Error(),
		},
		{
			name:         "already translated status error passes through without double-wrapping",
			err:          TranslateError(pgx.ErrNoRows, "experiment"),
			entity:       "work unit",
			wantCode:     codes.NotFound,
			wantSentinel: grpcerr.ErrNotFound,
			wantCause:    pgx.ErrNoRows,
			wantSubstr:   "experiment not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TranslateError(tt.err, tt.entity)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("TranslateError() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("TranslateError() = nil, want non-nil error")
			}
			if code := status.Code(got); code != tt.wantCode {
				t.Errorf("status.Code() = %v, want %v (err: %v)", code, tt.wantCode, got)
			}
			if tt.wantSentinel != nil && !errors.Is(got, tt.wantSentinel) {
				t.Errorf("errors.Is(%v, %v) = false, want true", got, tt.wantSentinel)
			}
			if tt.wantCause != nil && !errors.Is(got, tt.wantCause) {
				t.Errorf("errors.Is(%v, %v) = false, want true", got, tt.wantCause)
			}
			if !strings.Contains(got.Error(), tt.wantSubstr) {
				t.Errorf("Error() = %q, want substring %q", got.Error(), tt.wantSubstr)
			}
		})
	}
}

func TestErrorClassifiers(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantNotFound  bool
		wantUnique    bool
		wantFK        bool
		wantRetryable bool
	}{
		{
			name: "nil error",
			err:  nil,
		},
		{
			name:         "pgx.ErrNoRows",
			err:          pgx.ErrNoRows,
			wantNotFound: true,
		},
		{
			name:         "wrapped pgx.ErrNoRows",
			err:          fmt.Errorf("wrapped: %w", pgx.ErrNoRows),
			wantNotFound: true,
		},
		{
			name:         "translated ErrNoRows",
			err:          TranslateError(pgx.ErrNoRows, "experiment"),
			wantNotFound: true,
		},
		{
			name:         "grpcerr.ErrNotFound",
			err:          grpcerr.ErrNotFound,
			wantNotFound: true,
		},
		{
			name:       "unique_violation 23505",
			err:        &pgconn.PgError{Code: SQLStateUniqueViolation},
			wantUnique: true,
		},
		{
			name:       "translated unique_violation 23505",
			err:        TranslateError(&pgconn.PgError{Code: SQLStateUniqueViolation}, "xid"),
			wantUnique: true,
		},
		{
			name:   "foreign_key_violation 23503",
			err:    &pgconn.PgError{Code: SQLStateForeignKeyViolation},
			wantFK: true,
		},
		{
			name:   "translated foreign_key_violation 23503",
			err:    TranslateError(&pgconn.PgError{Code: SQLStateForeignKeyViolation}, "work unit"),
			wantFK: true,
		},
		{
			name:          "serialization_failure 40001",
			err:           &pgconn.PgError{Code: SQLStateSerializationFailure},
			wantRetryable: true,
		},
		{
			name:          "wrapped serialization_failure 40001",
			err:           fmt.Errorf("commit: %w", &pgconn.PgError{Code: SQLStateSerializationFailure}),
			wantRetryable: true,
		},
		{
			name:          "deadlock_detected 40P01",
			err:           &pgconn.PgError{Code: SQLStateDeadlockDetected},
			wantRetryable: true,
		},
		{
			name:          "translated deadlock_detected 40P01",
			err:           TranslateError(&pgconn.PgError{Code: SQLStateDeadlockDetected}, "experiment"),
			wantRetryable: true,
		},
		{
			name: "check_violation 23514",
			err:  &pgconn.PgError{Code: SQLStateCheckViolation},
		},
		{
			name: "unmapped error",
			err:  errors.New("other error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotFound(tt.err); got != tt.wantNotFound {
				t.Errorf("IsNotFound() = %v, want %v", got, tt.wantNotFound)
			}
			if got := IsUniqueViolation(tt.err); got != tt.wantUnique {
				t.Errorf("IsUniqueViolation() = %v, want %v", got, tt.wantUnique)
			}
			if got := IsForeignKeyViolation(tt.err); got != tt.wantFK {
				t.Errorf("IsForeignKeyViolation() = %v, want %v", got, tt.wantFK)
			}
			if got := IsRetryableTxError(tt.err); got != tt.wantRetryable {
				t.Errorf("IsRetryableTxError() = %v, want %v", got, tt.wantRetryable)
			}
		})
	}
}
