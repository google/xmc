// Copyright 2026 Google LLC
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
	"xmanager_cloud/internal/storage"
)

// mockAuthorResourceAllocations returns placeholder per-author rows in the shape storage will
// aggregate from active work units. The UI shows a work-in-progress banner while these are served.
func mockAuthorResourceAllocations() []storage.AuthorResourceAllocation {
	running, pending := storage.ActiveResourceStateRunning, storage.ActiveResourceStatePending
	return []storage.AuthorResourceAllocation{
		{Author: "alice@example.com", ResourceType: "cpu", State: running, Units: 32},
		{Author: "alice@example.com", ResourceType: "cpu", State: pending, Units: 16},
		{Author: "alice@example.com", ResourceType: "memory", State: running, Units: 128},
		{Author: "alice@example.com", ResourceType: "memory", State: pending, Units: 64},
		{Author: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", State: running, Units: 8},
		{Author: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", State: pending, Units: 4},
		{Author: "bob@example.com", ResourceType: "cpu", State: running, Units: 8},
		{Author: "bob@example.com", ResourceType: "memory", State: running, Units: 32},
		{Author: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-l4", State: running, Units: 4},
		{Author: "carol@example.com", ResourceType: "google.com/tpu: tpu-v5-lite-podslice", State: pending, Units: 8},
	}
}
