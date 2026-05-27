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

package proto

import "google.golang.org/protobuf/proto"

type Message = proto.Message

func Marshal(m Message) ([]byte, error)       { return proto.Marshal(m) }
func Unmarshal(b []byte, m Message) error     { return proto.Unmarshal(b, m) }
func Clone(m Message) Message                 { return proto.Clone(m) }
func Equal(x, y Message) bool                 { return proto.Equal(x, y) }
func Merge(dst, src Message)                  { proto.Merge(dst, src) }
func Size(m Message) int                      { return proto.Size(m) }
func String(s string) *string                 { return &s }
func Int64(v int64) *int64                    { return &v }
func Int32(v int32) *int32                    { return &v }
func Bool(v bool) *bool                       { return &v }
func Float64(v float64) *float64              { return &v }

