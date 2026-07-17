// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package logging

import (
	"context"
	"log/slog"

	"golang/grpc/metadata/metadata"
	"golang/uuid/uuid"
)

const (
	keyOperationGCP = "logging.googleapis.com/operation"
	keyOperationID  = "operation_id"
	requestIDHeader = "x-request-id"
)

// InjectOperation ensures an operation ID is present in the context.
// It retrieves it from various sources or generates a new one.
// It injects the ID into context for gRPC metadata propagation,
// and for structured logging compliant with GCP LogEntryOperation.
func InjectOperation(ctx context.Context, id string) context.Context {
	if id == "" {
		if ctxID, ok := ctx.Value(traceIDKey).(string); ok && ctxID != "" {
			id = ctxID
		} else if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(requestIDHeader); len(vals) > 0 && vals[0] != "" {
				id = vals[0]
			}
		}
	}
	if id == "" {
		id = uuid.NewString()
	}

	ctx = context.WithValue(ctx, traceIDKey, id)
	ctx = metadata.AppendToOutgoingContext(ctx, requestIDHeader, id)
	return WithValues(ctx, slog.Group(keyOperationGCP, slog.String("id", id)), slog.String(keyTrace, id))
}

// OperationID returns the operation ID from the context if it exists.
func OperationID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(traceIDKey).(string)
	return id, ok
}
