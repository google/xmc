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

import { toOrderBy } from './aip_sort';

describe('toOrderBy', () => {
  it('returns the field and ascending direction', () => {
    expect(toOrderBy({ active: 'state', direction: 'asc' })).toBe('state asc');
  });

  it('returns the field and descending direction', () => {
    expect(toOrderBy({ active: 'execution_completed', direction: 'desc' })).toBe(
      'execution_completed desc',
    );
  });

  it('returns undefined when the sort is cleared', () => {
    expect(toOrderBy({ active: 'state', direction: '' })).toBeUndefined();
  });
});
