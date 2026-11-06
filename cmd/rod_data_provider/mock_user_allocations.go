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
	spb "xmanager_cloud/api/protos/rod"
)

// mockUserAllocations returns placeholder per-user allocations until they are aggregated from
// active work units. The UI shows a work-in-progress banner while these are served.
func mockUserAllocations() []*spb.UserResourceAllocation {
	return []*spb.UserResourceAllocation{
		{User: "alice@example.com", ResourceType: "memory", RunningUnits: 128, PendingUnits: 64},
		{User: "alice@example.com", ResourceType: "cpu", RunningUnits: 32, PendingUnits: 16},
		{User: "bob@example.com", ResourceType: "memory", RunningUnits: 32},
		{User: "alice@example.com", ResourceType: "nvidia.com/gpu: nvidia-tesla-h100", RunningUnits: 8, PendingUnits: 4},
		{User: "bob@example.com", ResourceType: "cpu", RunningUnits: 8},
		{User: "carol@example.com", ResourceType: "google.com/tpu: tpu-v5-lite-podslice", PendingUnits: 8},
		{User: "bob@example.com", ResourceType: "nvidia.com/gpu: nvidia-l4", RunningUnits: 4},
	}
}
