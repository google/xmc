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

// Package bridge provides a test double for 3P custom resource status detection.
package bridge

import (
	"context"
	"sync"
)

// FakeStatusDetector is an in-memory test double for StatusDetector.
type FakeStatusDetector struct {
	mu        sync.Mutex
	Statuses  map[string]*ResourceStatus
	DetectErr error
	Calls     []WrapperRef
}

var _ StatusDetector = (*FakeStatusDetector)(nil)

// NewFakeStatusDetector creates a new FakeStatusDetector.
func NewFakeStatusDetector() *FakeStatusDetector {
	return &FakeStatusDetector{
		Statuses: make(map[string]*ResourceStatus),
	}
}

// SetStatus records the status to return for a given component key.
func (f *FakeStatusDetector) SetStatus(key string, status *ResourceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Statuses == nil {
		f.Statuses = make(map[string]*ResourceStatus)
	}
	f.Statuses[key] = status
}

// SetError configures a synthetic error to return from DetectStatus.
func (f *FakeStatusDetector) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DetectErr = err
}

// GetCalls returns a thread-safe copy of all WrapperRefs queried.
func (f *FakeStatusDetector) GetCalls() []WrapperRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]WrapperRef, len(f.Calls))
	copy(copied, f.Calls)
	return copied
}

// DetectStatus returns the preconfigured status or error for the requested resource.
func (f *FakeStatusDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, ref)
	if f.DetectErr != nil {
		return nil, f.DetectErr
	}
	key := ref.Name
	if ref.Namespace != "" {
		key = ref.Namespace + "/" + ref.Name
	}
	if st, ok := f.Statuses[key]; ok {
		return st, nil
	}
	if st, ok := f.Statuses[ref.Name]; ok {
		return st, nil
	}
	return &ResourceStatus{Found: false, Ready: false, Message: "resource not found"}, nil
}
