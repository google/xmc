package cmp

import "github.com/google/go-cmp/cmp"

type Option = cmp.Option

func Equal(x, y interface{}, opts ...Option) bool {
	return cmp.Equal(x, y, opts...)
}

func Diff(x, y interface{}, opts ...Option) string {
	return cmp.Diff(x, y, opts...)
}
