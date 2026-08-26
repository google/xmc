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
    return r.startsWith(`${f}:`) || r.startsWith(`${f}/`);
  }
  return r.includes(f);
}
