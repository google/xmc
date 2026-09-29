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

package crreader

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FakeGetter is an in-memory test double for Getter intended exclusively for unit testing.
type FakeGetter struct {
	Component  *unstructured.Unstructured
	Components map[string]*unstructured.Unstructured
	Err        error
}

var _ Getter = (*FakeGetter)(nil)

// GetComponent returns the configured error, the keyed Component from Components ("namespace/name" or "name"),
// or the fallback single Component field.
func (f *FakeGetter) GetComponent(_ context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	if f == nil {
		return nil, nil
	}
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Components != nil {
		if comp, ok := f.Components[namespace+"/"+name]; ok {
			return cloneUnstructured(comp), nil
		}
		if comp, ok := f.Components[name]; ok {
			return cloneUnstructured(comp), nil
		}
	}
	return cloneUnstructured(f.Component), nil
}

func cloneUnstructured(u *unstructured.Unstructured) *unstructured.Unstructured {
	if u == nil {
		return nil
	}
	return u.DeepCopy()
}
