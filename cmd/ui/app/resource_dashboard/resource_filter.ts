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

/**
 * Extracts the parent resource type from a compound resource identifier
 * (e.g. "nvidia.com/gpu: nvidia-tesla-a100" -> "nvidia.com/gpu").
 * Returns null if the resource is not compound or has no distinct parent.
 */
export function extractParentResourceType(resourceType: string): string | null {
  const trimmed = resourceType?.trim();
  if (!trimmed) {
    return null;
  }
  const colonIndex = trimmed.indexOf(':');
  if (colonIndex <= 0) {
    return null;
  }
  const parent = trimmed.substring(0, colonIndex).trim();
  const model = trimmed.substring(colonIndex + 1).trim();
  if (!parent || !model) {
    return null;
  }
  return parent;
}

/**
 * Expands a collection of resource types to include synthesized parent resource types
 * for any compound entries, returning a deduplicated and deterministically sorted list.
 */
export function expandResourceTypesWithParents(resourceTypes: Iterable<string>): string[] {
  const uniqueTypes = new Set<string>();
  for (const rawType of resourceTypes) {
    const trimmed = rawType?.trim();
    if (!trimmed) {
      continue;
    }
    uniqueTypes.add(trimmed);
    const parent = extractParentResourceType(trimmed);
    if (parent) {
      uniqueTypes.add(parent);
    }
  }
  return Array.from(uniqueTypes).sort();
}
