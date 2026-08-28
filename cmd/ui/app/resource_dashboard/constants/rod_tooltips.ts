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

/**
 * Static tooltips dictionary for Resource Observability Dashboard (ROD) term explanations.
 */
export const ROD_TERM_TOOLTIPS = {
  maxSchedulableNow:
    'Max Schedulable Now (Cohort quota)\n\nThe maximum single workload size that can be admitted immediately right now taking into account cohort borrowing (without waiting or preemption).',

  freeTotalCapacity:
    "Nominal Queue Quota (Free / Total)\n\nFree: Unused portion of this queue's guaranteed nominal quota.\n\nTotal: The baseline nominal quota configured for this queue.",

  resourceVelocity:
    'The rate at which hardware resources (e.g., GPUs) are being assigned to new workloads.\n\nIt represents the volume of hardware turning over to serve new requests over the selected timeframe (1h / 24h / 7d). A high Resource Velocity means large blocks of hardware are freeing up, which is useful for gauging if there is room for large-scale multi-node jobs.',

  jobVelocity:
    'The rate at which individual workloads are successfully leaving the queue and starting to run.\n\nIt represents the actual throughput of tasks clearing the line over the selected timeframe (1h / 24h / 7d). If Job Velocity is high but Resource Velocity is low, it indicates the queue is quickly processing many small, single-node jobs rather than a few massive ones.',

  pendingJobs:
    'The total number of workloads currently waiting for resources in this queue.\n\nIt includes all jobs that have been submitted but are not yet running. Use this alongside Resource Velocity and Job Velocity to gauge your relative position and potential wait time. If the velocity is 0, it may indicate that the queue is stuck or capacity is fully saturated by long-running jobs.',

  pendingResourceRequests:
    'The cumulative amount of hardware resources (e.g., GPUs) requested by all currently pending workloads.\n\nIt represents the total backlog demand waiting to be admitted to the queue. Comparing this number to your Total Capacity and Resource Velocity helps you estimate how many full cycles of the hardware pool are required to clear the current backlog.',
} as const;

export type RodTooltipKey = keyof typeof ROD_TERM_TOOLTIPS;
