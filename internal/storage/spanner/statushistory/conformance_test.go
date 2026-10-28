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

// External test package: spanner.NewStorage, which wires the repository under test, imports
// statushistory.
package statushistory_test

import (
	"testing"

	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/storage/storagetest"
)

func TestStatusHistoryConformance(t *testing.T) {
	storagetest.RunStatusHistoryTests(t, func(t *testing.T) storagetest.StatusHistoryBackend {
		_, essClient := emulator.Setup(t.Context(), t)
		store, err := spanner.NewStorage(essClient)
		if err != nil {
			t.Fatalf("spanner.NewStorage() failed: %v", err)
		}
		return store
	})
}
