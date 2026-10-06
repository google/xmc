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

// Package crreadertest provides concurrency-safe in-memory test doubles for pkg/components/crreader.
package crreadertest

import (
	"context"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/pkg/components/crreader"
)

// FakeGetter is a concurrency-safe in-memory test double for crreader.Getter.
type FakeGetter struct {
	mu         sync.RWMutex
	Component  *unstructured.Unstructured
	Components map[string]*unstructured.Unstructured
	Err        error
}

var _ crreader.Getter = (*FakeGetter)(nil)

// GetComponent returns the configured error, the keyed Component from Components ("namespace/name" or "name"),
// the fallback single Component field, or a Kubernetes NotFound error if no component matches.
func (f *FakeGetter) GetComponent(_ context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	if f == nil {
		return nil, apierrors.NewNotFound(crreader.ComponentGVR().GroupResource(), name)
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.Err != nil {
		return nil, f.Err
	}
	if f.Components != nil {
		if comp, ok := f.Components[namespace+"/"+name]; ok && comp != nil {
			return comp.DeepCopy(), nil
		}
		if comp, ok := f.Components[name]; ok && comp != nil {
			return comp.DeepCopy(), nil
		}
	}
	if f.Component != nil {
		return f.Component.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(crreader.ComponentGVR().GroupResource(), name)
}
