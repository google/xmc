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

import { buildActiveJobsFilter } from './aip_filter';

describe('buildActiveJobsFilter', () => {
  it('returns empty string when both author and status are empty', () => {
    expect(buildActiveJobsFilter('', '')).toBe('');
  });

  it('formats author predicate with JSON escaping', () => {
    expect(buildActiveJobsFilter('alice', '')).toBe('author = "alice"');
  });

  it('formats status predicate', () => {
    expect(buildActiveJobsFilter('', 'RUNNING')).toBe('status = RUNNING');
  });

  it('joins author and status predicates with AND', () => {
    expect(buildActiveJobsFilter('alice', 'SCHEDULING')).toBe(
      'author = "alice" AND status = SCHEDULING',
    );
  });
});
