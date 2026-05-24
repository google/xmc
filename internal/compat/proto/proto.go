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

