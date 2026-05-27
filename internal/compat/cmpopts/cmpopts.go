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

package cmpopts

import (
	"golang/cmp/cmp"

	"github.com/google/go-cmp/cmp/cmpopts"
)

func IgnoreFields(typ interface{}, names ...string) cmp.Option {
	return cmpopts.IgnoreFields(typ, names...)
}

func IgnoreUnexported(typs ...interface{}) cmp.Option {
	return cmpopts.IgnoreUnexported(typs...)
}

func SortSlices(less interface{}) cmp.Option {
	return cmpopts.SortSlices(less)
}

func EquateEmpty() cmp.Option {
	return cmpopts.EquateEmpty()
}
