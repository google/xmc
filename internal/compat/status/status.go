package status

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
)
