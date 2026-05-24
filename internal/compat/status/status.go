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

func CodeFromError(err error) Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
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
