// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"errors"
	"testing"
)

func TestManager_HealthCheckers_NilManager(t *testing.T) {
	var m *Manager
	k8s, sp := m.HealthCheckers()
	if k8s != nil {
		t.Errorf("HealthCheckers() k8sClient = %v, want nil", k8s)
	}
	if sp != nil {
		t.Errorf("HealthCheckers() spannerClient = %v, want nil", sp)
	}
}

func TestManager_Close(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		var m *Manager
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("nil cleanup", func(t *testing.T) {
		m := &Manager{}
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("cleanup succeeds", func(t *testing.T) {
		cleaned := false
		m := &Manager{
			cleanup: func() error {
				cleaned = true
				return nil
			},
		}
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
		if !cleaned {
			t.Error("Close() did not invoke cleanup function")
		}
	})

	t.Run("cleanup returns error", func(t *testing.T) {
		expectedErr := errors.New("cleanup failed")
		m := &Manager{
			cleanup: func() error {
				return expectedErr
			},
		}
		err := m.Close()
		if !errors.Is(err, expectedErr) {
			t.Errorf("Close() error = %v, want %v", err, expectedErr)
		}
	})

	t.Run("cleanup is idempotent", func(t *testing.T) {
		cleanCount := 0
		m := &Manager{
			cleanup: func() error {
				cleanCount++
				return nil
			},
		}
		if err := m.Close(); err != nil {
			t.Errorf("First Close() error = %v, want nil", err)
		}
		if err := m.Close(); err != nil {
			t.Errorf("Second Close() error = %v, want nil", err)
		}
		if cleanCount != 1 {
			t.Errorf("cleanup called %d times, want 1", cleanCount)
		}
	})
}
