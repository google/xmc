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
 * Builders for the AIP-160 filter and AIP-132 order-by strings sent by the work units table.
 */

/** A sort on one field; direction '' means no explicit order (server default). */
export interface AipSort {
  readonly active: string;
  readonly direction: 'asc' | 'desc' | '';
}

/** Builds an AIP-160 filter matching any of `values` on `field`, or undefined for none. */
export function buildAnyOfFilter(field: string, values: readonly string[]): string | undefined {
  if (values.length === 0) {
    return undefined;
  }
  return values.map((value) => `${field} = "${value}"`).join(' OR ');
}

/** Converts a sort into an AIP-132 orderBy, or undefined when the sort is cleared. */
export function toOrderBy(sort: AipSort): string | undefined {
  return sort.direction ? `${sort.active} ${sort.direction}` : undefined;
}
