// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package postgresdata

import (
	"google.golang.org/protobuf/proto"
	anypb "google.golang.org/protobuf/types/known/anypb"
)

// ProtoPointer constrains PT to a pointer to T that implements proto.Message.
type ProtoPointer[T any] interface {
	*T
	proto.Message
}

// MarshalProto serializes msg to a non-nil byte slice for NOT NULL BYTEA columns.
func MarshalProto(msg proto.Message) ([]byte, error) {
	b, err := proto.Marshal(msg)
	if err != nil || b != nil {
		return b, err
	}
	return []byte{}, nil
}

// MarshalNullableProto serializes msg when non-nil, or returns (nil, nil) so pgx binds SQL NULL.
func MarshalNullableProto[T any, PT ProtoPointer[T]](msg PT) ([]byte, error) {
	if msg == nil {
		return nil, nil
	}
	return MarshalProto(msg)
}

// UnmarshalProto deserializes b into a new *T.
func UnmarshalProto[T any, PT ProtoPointer[T]](b []byte) (PT, error) {
	msg := PT(new(T))
	return msg, proto.Unmarshal(b, msg)
}

// MarshalAnyPayload unpacks a into *T and serializes the underlying protobuf payload for BYTEA storage.
func MarshalAnyPayload[T any, PT ProtoPointer[T]](a *anypb.Any, allowNil bool) ([]byte, error) {
	if a == nil && allowNil {
		return nil, nil
	}
	msg := PT(new(T))
	if a != nil {
		if err := a.UnmarshalTo(msg); err != nil {
			return nil, err
		}
	}
	return MarshalProto(msg)
}

// UnmarshalToAny deserializes b into *T and packs it into a google.protobuf.Any message.
func UnmarshalToAny[T any, PT ProtoPointer[T]](b []byte) (*anypb.Any, error) {
	msg, err := UnmarshalProto[T, PT](b)
	if err != nil {
		return nil, err
	}
	return anypb.New(msg)
}
