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

// Package grpcerr defines canonical gRPC status sentinel errors used for
// errors.Is matching across storage, domain, and RPC boundaries.
package grpcerr

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Canonical gRPC status sentinel errors.
var (
	ErrInvalidArgument    = status.Error(codes.InvalidArgument, "invalid argument")
	ErrNotFound           = status.Error(codes.NotFound, "not found")
	ErrAlreadyExists      = status.Error(codes.AlreadyExists, "already exists")
	ErrPermissionDenied   = status.Error(codes.PermissionDenied, "permission denied")
	ErrInternal           = status.Error(codes.Internal, "internal error")
	ErrUnimplemented      = status.Error(codes.Unimplemented, "unimplemented")
	ErrFailedPrecondition = status.Error(codes.FailedPrecondition, "failed precondition")
	ErrAborted            = status.Error(codes.Aborted, "aborted")
	ErrUnavailable        = status.Error(codes.Unavailable, "unavailable")
	ErrDeadlineExceeded   = status.Error(codes.DeadlineExceeded, "deadline exceeded")
	ErrUnauthenticated    = status.Error(codes.Unauthenticated, "unauthenticated")
)
