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
export function setPageSize<T extends {pagination: PaginationState}>(
    state: T, pageSize: number): T {
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
