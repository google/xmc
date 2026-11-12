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

/** A sort on one field; direction '' means no explicit order (server default). */
export interface AipSort {
  readonly active: string;
  readonly direction: 'asc' | 'desc' | '';
}

/** Converts a sort into an AIP-132 orderBy, or undefined when the sort is cleared. */
export function toOrderBy(sort: AipSort): string | undefined {
  return sort.direction ? `${sort.active} ${sort.direction}` : undefined;
}
