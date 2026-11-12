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

/** A single field = value predicate in an AIP-160 filter; empty values are omitted. */
export interface AipFilterPredicate {
  readonly field: string;
  readonly value: string;
  readonly quoted?: boolean;
}

/** Joins non-empty equality predicates into an AIP-160 AND filter string. */
export function toAndFilter(predicates: readonly AipFilterPredicate[]): string {
  return predicates
    .filter((p) => p.value)
    .map((p) => `${p.field} = ${p.quoted ? JSON.stringify(p.value) : p.value}`)
    .join(' AND ');
}
