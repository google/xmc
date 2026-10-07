// Copyright 2026 Google LLC
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

// Package safesql provides a compile-time constant SQL type that prevents
// untrusted runtime strings from being interpolated into SQL statements.
package safesql

import (
	"strconv"
	"strings"
)

// stringConstant is an unexported string type used to ensure that New and
// StringJoin only accept untyped compile-time string constants.
type stringConstant string

// TrustedSQLString represents a SQL query or fragment guaranteed to originate
// from compile-time constants and safe parameter placeholders.
type TrustedSQLString struct {
	s string
}

// New constructs a TrustedSQLString from an untyped compile-time string constant.
// Passing a runtime string variable fails to compile.
func New(s stringConstant) TrustedSQLString {
	return TrustedSQLString{s: string(s)}
}

// EmptyString returns an empty TrustedSQLString.
func EmptyString() TrustedSQLString {
	return TrustedSQLString{s: ""}
}

// Concat concatenates multiple TrustedSQLString fragments into a single TrustedSQLString.
func Concat(parts ...TrustedSQLString) TrustedSQLString {
	var totalLen int
	for _, p := range parts {
		totalLen += len(p.s)
	}
	var b strings.Builder
	b.Grow(totalLen)
	for _, p := range parts {
		b.WriteString(p.s)
	}
	return TrustedSQLString{s: b.String()}
}

// StringJoin joins a slice of TrustedSQLString fragments with a compile-time constant separator.
func StringJoin(sep stringConstant, parts ...TrustedSQLString) TrustedSQLString {
	if len(parts) == 0 {
		return EmptyString()
	}
	var totalLen int
	for _, p := range parts {
		totalLen += len(p.s)
	}
	totalLen += len(sep) * (len(parts) - 1)

	var b strings.Builder
	b.Grow(totalLen)
	b.WriteString(parts[0].s)
	for _, p := range parts[1:] {
		b.WriteString(string(sep))
		b.WriteString(p.s)
	}
	return TrustedSQLString{s: b.String()}
}

// NewBindingParam constructs a TrustedSQLString for a numbered parameter placeholder
// (e.g. "@fb0") from a compile-time constant prefix and an unsigned integer index.
func NewBindingParam(prefix stringConstant, index uint64) TrustedSQLString {
	return TrustedSQLString{s: string(prefix) + strconv.FormatUint(index, 10)}
}

// String returns the underlying SQL string.
func (t TrustedSQLString) String() string {
	return t.s
}
