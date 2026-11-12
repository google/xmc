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

import { toAndFilter } from './aip_filter';

describe('toAndFilter', () => {
  it('returns empty string when all predicate values are empty', () => {
    expect(
      toAndFilter([
        { field: 'author', value: '', quoted: true },
        { field: 'status', value: '' },
      ]),
    ).toBe('');
  });

  it('formats a quoted string predicate with JSON escaping', () => {
    expect(toAndFilter([{ field: 'author', value: 'alice', quoted: true }])).toBe(
      'author = "alice"',
    );
  });

  it('formats an unquoted enum/identifier predicate', () => {
    expect(toAndFilter([{ field: 'status', value: 'RUNNING' }])).toBe('status = RUNNING');
  });

  it('joins multiple non-empty predicates with AND', () => {
    expect(
      toAndFilter([
        { field: 'author', value: 'alice', quoted: true },
        { field: 'status', value: 'SCHEDULING' },
      ]),
    ).toBe('author = "alice" AND status = SCHEDULING');
  });
});
