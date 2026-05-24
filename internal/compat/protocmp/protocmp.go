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

func IgnoreFields(message protocmp.Message, names ...string) cmp.Option {
	fields := make([]protoreflect.Name, len(names))
	for i, n := range names {
		fields[i] = protoreflect.Name(n)
	}
	return protocmp.IgnoreFields(message, fields...)
}

func IgnoreMessages(messages ...proto.Message) cmp.Option {
	return protocmp.IgnoreMessages(messages...)
}
