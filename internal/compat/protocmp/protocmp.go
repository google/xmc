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

package protocmp

import (
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
)

type Message = protocmp.Message

func Transform() cmp.Option {
	return protocmp.Transform()
}

func IgnoreFields(message proto.Message, names ...string) cmp.Option {
	fields := make([]protoreflect.Name, len(names))
	for i, n := range names {
		fields[i] = protoreflect.Name(n)
	}
	return protocmp.IgnoreFields(message, fields...)
}

func IgnoreMessages(messages ...proto.Message) cmp.Option {
	return protocmp.IgnoreMessages(messages...)
}
