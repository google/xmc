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

package cmp

import "github.com/google/go-cmp/cmp"

type Option = cmp.Option

func Equal(x, y interface{}, opts ...Option) bool {
	return cmp.Equal(x, y, opts...)
}

func Diff(x, y interface{}, opts ...Option) string {
	return cmp.Diff(x, y, opts...)
}

func AllowUnexported(types ...interface{}) Option {
	return cmp.AllowUnexported(types...)
}
