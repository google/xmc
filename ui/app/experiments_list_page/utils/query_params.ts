import {Params} from '@angular/router';

/** Experiment filters query param keys. */
enum ExperimentFiltersQueryParams {
  FILTER = 'filter',
}

/** Converts ExperimentFilters to query params. */
export function experimentFiltersToQueryParams(filter: string): Params {
  return {
    [ExperimentFiltersQueryParams.FILTER]: filter || undefined,
  };
}

/** Converts query params to ExperimentFilters. */
export function queryParamsToExperimentFilters(queryParams: Params): string {
  return queryParams[ExperimentFiltersQueryParams.FILTER] || undefined;
}
