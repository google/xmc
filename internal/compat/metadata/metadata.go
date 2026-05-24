package metadata

import (
	"context"

	"google.golang.org/grpc/metadata"
)

type MD = metadata.MD

func New(m map[string]string) MD                                     { return metadata.New(m) }
func Pairs(kv ...string) MD                                         { return metadata.Pairs(kv...) }
func FromIncomingContext(ctx context.Context) (MD, bool)             { return metadata.FromIncomingContext(ctx) }
func FromOutgoingContext(ctx context.Context) (MD, bool)             { return metadata.FromOutgoingContext(ctx) }
func NewIncomingContext(ctx context.Context, md MD) context.Context { return metadata.NewIncomingContext(ctx, md) }
func NewOutgoingContext(ctx context.Context, md MD) context.Context { return metadata.NewOutgoingContext(ctx, md) }
func AppendToOutgoingContext(ctx context.Context, kv ...string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, kv...)
}
