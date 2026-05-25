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

