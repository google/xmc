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
/** Pagination state shape. */
export interface PaginationState {
  currentPageToken: string;
  previousTokens: string[];
  nextPageToken?: string;
  pageSize: number;
  pageIndex: number;
}

/** Initial pagination state. */
export function getInitialPaginationState(pageSize = 10): PaginationState {
  return {
    currentPageToken: '',
    previousTokens: [],
    nextPageToken: undefined,
    pageSize,
    pageIndex: 0,
  };
}

/**
 * Reducer function for updating page size in a state object that has a
 * pagination property.
 */
export function setPageSize<T extends { pagination: PaginationState }>(
  state: T,
  pageSize: number,
): T {
  return {
    ...state,
    pagination: {
      ...state.pagination,
      pageSize,
      pageIndex: 0,
      currentPageToken: '',
      previousTokens: [],
      nextPageToken: undefined,
    },
  };
}

/**
 * Reducer function for going to the next page.
 */
export function goToNextPage<T extends { pagination: PaginationState }>(state: T): T {
  const { pagination } = state;

  if (!pagination.nextPageToken) {
    return state;
  }

  return {
    ...state,
    pagination: {
      ...pagination,
      pageIndex: pagination.pageIndex + 1,
      currentPageToken: pagination.nextPageToken,
      previousTokens: [...pagination.previousTokens, pagination.currentPageToken],
      nextPageToken: undefined,
    },
  };
}

/**
 * Reducer function for going to the previous page.
 */
export function goToPreviousPage<T extends { pagination: PaginationState }>(state: T): T {
  const { pagination } = state;
  const { previousTokens, pageIndex } = pagination;

  if (previousTokens.length === 0) {
    return state;
  }

  return {
    ...state,
    pagination: {
      ...pagination,
      currentPageToken: previousTokens.at(-1) ?? '',
      previousTokens: previousTokens.slice(0, -1),
      pageIndex: pageIndex - 1,
    },
  };
}
