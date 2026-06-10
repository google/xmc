// Copyright 2025 Google LLC
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

package status

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Code = codes.Code

const (
	OK                 Code = codes.OK
	Canceled           Code = codes.Canceled
	Unknown            Code = codes.Unknown
	InvalidArgument    Code = codes.InvalidArgument
	DeadlineExceeded   Code = codes.DeadlineExceeded
	NotFound           Code = codes.NotFound
	AlreadyExists      Code = codes.AlreadyExists
	PermissionDenied   Code = codes.PermissionDenied
	ResourceExhausted  Code = codes.ResourceExhausted
	FailedPrecondition Code = codes.FailedPrecondition
	Aborted            Code = codes.Aborted
	OutOfRange         Code = codes.OutOfRange
	Unimplemented      Code = codes.Unimplemented
	Internal           Code = codes.Internal
	Unavailable        Code = codes.Unavailable
	DataLoss           Code = codes.DataLoss
	Unauthenticated    Code = codes.Unauthenticated
)

func Errorf(code Code, format string, a ...interface{}) error {
	return status.Errorf(code, format, a...)
}

func Error(code Code, msg string) error {
	return status.Error(code, msg)
}

type Status struct {
	Code    Code
	Message string
	Msg     string
}

func FromError(err error) (*Status, bool) {
	if s, ok := status.FromError(err); ok {
		return &Status{
			Code:    s.Code(),
			Message: s.Message(),
			Msg:     s.Message(),
		}, true
	}
	return nil, false
}

func (m *Status) CanonicalCode() Code {
	if m == nil {
		return codes.Unknown
	}
	return m.Code
}

func (m *Status) Err() error {
	if m == nil {
		return nil
	}
	return status.Error(m.Code, m.Message)
}

func CodeFromError(err error) Code {
	if s, ok := FromError(err); ok {
		return s.Code
	}
	return codes.Unknown
}

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

func ExtractCanonicalCode(err error) Code {
	return CodeFromError(err)
}
