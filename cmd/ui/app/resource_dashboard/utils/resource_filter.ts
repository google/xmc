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

function isGenericPrefixMatch(resource: string, filter: string): boolean {
  if (resource.startsWith(filter + ':') || resource.startsWith(filter + '/')) {
    return true;
  }
  return (filter === 'gpu' || filter === 'tpu') && resource.includes(filter);
}

function isModelSubstringMatch(resource: string, filter: string): boolean {
  if (filter.includes(':') || filter.includes('/')) {
    return false;
  }
  return resource.includes(filter);
}

/**
 * Checks if a queue resource type name matches a given user resource filter.
 *
 * Matching behavior:
 * 1. Exact match (case-insensitive).
 * 2. Generic filter (e.g. "nvidia.com/gpu", "gpu", "google.com/tpu", "tpu"):
 *    Matches both generic and specific resources under that domain (e.g. "nvidia.com/gpu: nvidia-tesla-a100").
 * 3. Specific model filter (e.g. "nvidia.com/gpu: nvidia-tesla-a100"):
 *    Matches only that specific model and does NOT match generic queues (e.g. "nvidia.com/gpu").
 */
export function matchesResourceFilter(resourceType: string, filter: string): boolean {
  if (!resourceType || !filter) {
    return false;
  }
  const lowerResource = resourceType.trim().toLowerCase();
  const lowerFilter = filter.trim().toLowerCase();

  return (
    lowerResource === lowerFilter ||
    isGenericPrefixMatch(lowerResource, lowerFilter) ||
    isModelSubstringMatch(lowerResource, lowerFilter)
  );
}
