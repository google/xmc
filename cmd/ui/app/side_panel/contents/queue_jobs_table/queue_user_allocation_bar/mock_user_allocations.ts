// Copyright 2026 Google LLC
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

import { UserResourceAllocation } from 'app/protos/rod';

/**
 * Placeholder per-user allocations rendered until GetRunningAndPendingJobs returns
 * user_allocations. The jobs card shows a work-in-progress banner while this is in use.
 */
export const MOCK_USER_ALLOCATIONS: readonly UserResourceAllocation[] = [
  {
    user: 'alice@example.com',
    resourceType: 'nvidia.com/gpu: nvidia-tesla-h100',
    runningUnits: 8,
    pendingUnits: 4,
  },
  { user: 'bob@example.com', resourceType: 'nvidia.com/gpu: nvidia-l4', runningUnits: 4 },
  { user: 'alice@example.com', resourceType: 'cpu', runningUnits: 32, pendingUnits: 16 },
  { user: 'bob@example.com', resourceType: 'cpu', runningUnits: 8 },
  { user: 'alice@example.com', resourceType: 'memory', runningUnits: 128, pendingUnits: 64 },
  { user: 'bob@example.com', resourceType: 'memory', runningUnits: 32 },
  {
    user: 'carol@example.com',
    resourceType: 'google.com/tpu: tpu-v5-lite-podslice',
    pendingUnits: 8,
  },
];
