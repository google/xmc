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

function matchesDelimitedPrefix(resource: string, filter: string): boolean {
  if (!resource.startsWith(filter)) {
    return false;
  }
  if (filter.endsWith(':') || filter.endsWith('/')) {
    return true;
  }
  const nextChar = resource.charAt(filter.length);
  return nextChar === ':' || nextChar === '/';
}

export function matchesResourceFilter(resourceType: string, filter: string): boolean {
  const r = resourceType?.trim().toLowerCase();
  const f = filter?.trim().toLowerCase();
  if (!r || !f) {
    return false;
  }
  if (r === f) {
    return true;
  }
  if (f.includes(':') || f.includes('/')) {
    return matchesDelimitedPrefix(r, f);
  }
  return r.includes(f);
}

// Delimiter emitted by the ROD backend's resourceaccumulator.FormatCompoundAccelerator
// (e.g. "nvidia.com/gpu: nvidia-tesla-a100").
const COMPOUND_RESOURCE_DELIMITER = ': ';

function parentResourceType(resourceType: string): string | null {
  const delimiterIndex = resourceType.indexOf(COMPOUND_RESOURCE_DELIMITER);
  return delimiterIndex > 0 ? resourceType.slice(0, delimiterIndex) : null;
}

/**
 * Adds the generic parent (e.g. "nvidia.com/gpu") of every compound accelerator type
 * and returns the deduplicated, sorted list, so a parent sorts before its models.
 */
export function expandResourceTypesWithParents(resourceTypes: string[]): string[] {
  const parents = resourceTypes
    .map(parentResourceType)
    .filter((parent): parent is string => parent !== null);
  return [...new Set([...resourceTypes, ...parents])].sort();
}
