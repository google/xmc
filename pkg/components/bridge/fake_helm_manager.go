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

package bridge

import (
	"context"
	"errors"
	"sync"
)

// FakeHelmManager provides an in-memory double of HelmManager for unit tests.
type FakeHelmManager struct {
	mu               sync.Mutex
	ActiveComponents map[string]bool
	AppliedValues    map[string]map[string]any
	EnsureErr        error
	DeleteErr        error
}

// NewFakeHelmManager creates an initialized FakeHelmManager.
func NewFakeHelmManager() *FakeHelmManager {
	return &FakeHelmManager{
		ActiveComponents: make(map[string]bool),
		AppliedValues:    make(map[string]map[string]any),
	}
}

// Ensure1PComponent records component enablement and synthesized Helm values.
func (f *FakeHelmManager) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if f == nil {
		return nil
	}
	if ref.Name == "" {
		return errors.New("component name must not be empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	if f.ActiveComponents == nil {
		f.ActiveComponents = make(map[string]bool)
	}
	if f.AppliedValues == nil {
		f.AppliedValues = make(map[string]map[string]any)
	}
	f.ActiveComponents[ref.String()] = opts.Enabled
	f.AppliedValues[ref.String()] = BuildHelmValues(opts)
	return nil
}

// Delete1PComponent records component disabling in memory while retaining values with enabled: false.
func (f *FakeHelmManager) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	if f == nil {
		return nil
	}
	if ref.Name == "" {
		return errors.New("component name must not be empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveComponents != nil {
		f.ActiveComponents[ref.String()] = false
	}
	if f.AppliedValues != nil {
		f.disableAppliedValues(ref.Name, ref.String())
	}
	return nil
}

func (f *FakeHelmManager) disableAppliedValues(name, key string) {
	deploymentKey := ComponentNameToHelmKey(name)
	val, exists := f.AppliedValues[key]
	if !exists {
		f.AppliedValues[key] = map[string]any{
			deploymentKey: map[string]any{"enabled": false},
		}
		return
	}
	dep, ok := val[deploymentKey].(map[string]any)
	if !ok {
		dep = make(map[string]any)
		val[deploymentKey] = dep
	}
	dep["enabled"] = false
}

// Purge1PComponent permanently removes component values and tracking in memory.
func (f *FakeHelmManager) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	if f == nil {
		return nil
	}
	if ref.Name == "" {
		return errors.New("component name must not be empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveComponents != nil {
		delete(f.ActiveComponents, ref.String())
	}
	if f.AppliedValues != nil {
		delete(f.AppliedValues, ref.String())
	}
	return nil
}

// Orphan1PComponent detaches tracking without removing values.
func (f *FakeHelmManager) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if f == nil {
		return nil
	}
	if ref.Name == "" {
		return errors.New("component name must not be empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveComponents != nil {
		delete(f.ActiveComponents, ref.String())
	}
	return nil
}

// IsComponentActive checks whether the component is recorded as active.
func (f *FakeHelmManager) IsComponentActive(ref ComponentRef) bool {
	if f == nil || ref.Name == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ActiveComponents[ref.String()]
}

// GetAppliedValues returns the applied Helm values for a component thread-safely.
func (f *FakeHelmManager) GetAppliedValues(ref ComponentRef) map[string]any {
	if f == nil || ref.Name == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.AppliedValues[ref.String()]
}
