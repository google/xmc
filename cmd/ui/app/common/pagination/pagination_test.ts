// Copyright 2025 Google LLC
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
import 'jasmine';

import { getInitialPaginationState, PaginationState, setPageSize } from './pagination';

describe('Pagination', () => {
  describe('getInitialPaginationState', () => {
    it('should return initial state with default page size', () => {
      const expectedState: PaginationState = {
        currentPageToken: '',
        previousTokens: [],
        nextPageToken: undefined,
        pageSize: 10,
        pageIndex: 0,
      };
      expect(getInitialPaginationState()).toEqual(expectedState);
    });

    it('should return initial state with specified page size', () => {
      const expectedState: PaginationState = {
        currentPageToken: '',
        previousTokens: [],
        nextPageToken: undefined,
        pageSize: 20,
        pageIndex: 0,
      };
      expect(getInitialPaginationState(20)).toEqual(expectedState);
    });
  });

  describe('setPageSize', () => {
    it('should update page size and reset pagination', () => {
      const state: { pagination: PaginationState } = {
        pagination: {
          currentPageToken: 'abc',
          previousTokens: [''],
          nextPageToken: 'def',
          pageSize: 10,
          pageIndex: 1,
        },
      };
      const expectedState = {
        pagination: {
          currentPageToken: '',
          previousTokens: [],
          nextPageToken: undefined,
          pageSize: 50,
          pageIndex: 0,
        },
      };
      expect(setPageSize(state, 50)).toEqual(expectedState);
    });
  });
});
