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
