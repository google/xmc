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

// Package postgres provides PostgreSQL connection pooling, transaction management,
// and SQLSTATE error translation for XMC storage repositories.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

// PostgreSQL SQLSTATE error codes used for storage error translation.
const (
	SQLStateInvalidTextRepresentation = "22P02"
	SQLStateForeignKeyViolation       = "23503"
	SQLStateUniqueViolation           = "23505"
	SQLStateCheckViolation            = "23514"
	SQLStateSerializationFailure      = "40001"
	SQLStateDeadlockDetected          = "40P01"
)

// TranslateError maps PostgreSQL driver and SQLSTATE errors into canonical storage status errors.
func TranslateError(err error, entity string) error {
	if err == nil || isPassthroughError(err) {
		return err
	}
	name := normalizeEntity(entity)
	if errors.Is(err, pgx.ErrNoRows) {
		return wrapStatusError(fmt.Sprintf("%s not found: %v", name, err), grpcerr.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if translated := translatePgError(pgErr, err, name); translated != nil {
			return translated
		}
	}
	return fmt.Errorf("%s storage error: %w", name, err)
}

func isPassthroughError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	_, ok := status.FromError(err)
	return ok
}

func translatePgError(pgErr *pgconn.PgError, cause error, entity string) error {
	switch pgErr.Code {
	case SQLStateUniqueViolation:
		return wrapStatusError(fmt.Sprintf("%s already exists: %v", entity, cause), grpcerr.ErrAlreadyExists, cause)
	case SQLStateForeignKeyViolation:
		return wrapStatusError(fmt.Sprintf("%s foreign key violation: %v", entity, cause), grpcerr.ErrFailedPrecondition, cause)
	case SQLStateCheckViolation, SQLStateInvalidTextRepresentation:
		return wrapStatusError(fmt.Sprintf("invalid %s: %v", entity, cause), grpcerr.ErrInvalidArgument, cause)
	case SQLStateSerializationFailure, SQLStateDeadlockDetected:
		return wrapStatusError(fmt.Sprintf("%s transaction aborted: %v", entity, cause), grpcerr.ErrAborted, cause)
	default:
		return nil
	}
}

// IsNotFound reports whether err represents a missing row or NotFound status error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) == codes.NotFound
}

// IsUniqueViolation reports whether err wraps a PostgreSQL unique_violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	return hasPgErrorCode(err, SQLStateUniqueViolation)
}

// IsForeignKeyViolation reports whether err wraps a PostgreSQL foreign_key_violation (SQLSTATE 23503).
func IsForeignKeyViolation(err error) bool {
	return hasPgErrorCode(err, SQLStateForeignKeyViolation)
}

// IsRetryableTxError reports whether err wraps a transient PostgreSQL serialization_failure (40001)
// or deadlock_detected (40P01) error that can be retried in a new transaction.
func IsRetryableTxError(err error) bool {
	return hasPgErrorCode(err, SQLStateSerializationFailure, SQLStateDeadlockDetected)
}

func hasPgErrorCode(err error, codes ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	for _, code := range codes {
		if pgErr.Code == code {
			return true
		}
	}
	return false
}

func normalizeEntity(entity string) string {
	trimmed := strings.TrimSpace(entity)
	if trimmed == "" {
		return "entity"
	}
	return trimmed
}

type statusError struct {
	msg      string
	sentinel error
	cause    error
}

func wrapStatusError(msg string, sentinel, cause error) error {
	return &statusError{
		msg:      msg,
		sentinel: sentinel,
		cause:    cause,
	}
}

func (e *statusError) Error() string {
	return e.msg
}

func (e *statusError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.sentinel}
	}
	return []error{e.sentinel, e.cause}
}

func (e *statusError) GRPCStatus() *status.Status {
	return status.New(status.Code(e.sentinel), e.msg)
}
