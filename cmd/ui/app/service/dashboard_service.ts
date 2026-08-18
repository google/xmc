// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { HttpClient, HttpParams } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Observable, throwError } from 'rxjs';
import { map } from 'rxjs/operators';

import { XMC_API_DOMAIN } from './constants';
import {
  Chart,
  ChartData,
  CreateChartRequest,
  CreateDashboardRequest,
  Dashboard,
  GetChartDataRequest,
  ListDashboardsRequest,
  ListDashboardsResponse,
  RawChart,
  RawChartData,
  RawDashboard,
  UpdateDashboardRequest,
} from './dashboard_models';
import { normalizeChart, normalizeChartData, normalizeDashboard } from './dashboard_normalizer';

/**
 * Service for communicating with the XMC Dashboard Service.
 */
@Injectable({ providedIn: 'root' })
export class DashboardService {
  private readonly http = inject(HttpClient);

  /**
   * Fetches a dashboard by its resource name.
   */
  getDashboard(name: string): Observable<Dashboard> {
    if (!name) {
      return throwError(() => new Error('Dashboard resource name is required'));
    }
    return this.http
      .get<RawDashboard>(`${XMC_API_DOMAIN}/${name}`, { withCredentials: true })
      .pipe(map(normalizeDashboard));
  }

  /**
   * Lists available dashboards with optional pagination and filtering.
   */
  listDashboards(request: ListDashboardsRequest = {}): Observable<ListDashboardsResponse> {
    const paramsMap: Record<string, string> = {};
    if (request.pageSize) {
      paramsMap['pageSize'] = request.pageSize.toString();
    }
    if (request.pageToken) {
      paramsMap['pageToken'] = request.pageToken;
    }
    if (request.filter) {
      paramsMap['filter'] = request.filter;
    }
    const params = new HttpParams({ fromObject: paramsMap });
    return this.http
      .get<{
        dashboards?: RawDashboard[];
        nextPageToken?: string;
        next_page_token?: string;
      }>(`${XMC_API_DOMAIN}/dashboards`, { params, withCredentials: true })
      .pipe(
        map((res) => ({
          dashboards: (res?.dashboards ?? []).map(normalizeDashboard),
          nextPageToken: res?.nextPageToken ?? res?.next_page_token,
        })),
      );
  }

  /**
   * Creates a new dashboard.
   */
  createDashboard(request: CreateDashboardRequest): Observable<Dashboard> {
    if (!request.dashboard) {
      return throwError(() => new Error('Dashboard payload is required'));
    }
    const paramsMap: Record<string, string> = {};
    if (request.dashboardId) {
      paramsMap['dashboardId'] = request.dashboardId;
    }
    const params = new HttpParams({ fromObject: paramsMap });
    return this.http
      .post<RawDashboard>(`${XMC_API_DOMAIN}/dashboards`, request.dashboard, {
        params,
        withCredentials: true,
      })
      .pipe(map(normalizeDashboard));
  }

  /**
   * Updates an existing dashboard.
   */
  updateDashboard(request: UpdateDashboardRequest): Observable<Dashboard> {
    if (!request.dashboard || !request.dashboard.name) {
      return throwError(() => new Error('Dashboard and resource name are required for update'));
    }
    const paramsMap: Record<string, string> = {};
    if (request.updateMask) {
      paramsMap['updateMask'] = request.updateMask;
    }
    const params = new HttpParams({ fromObject: paramsMap });
    return this.http
      .patch<RawDashboard>(`${XMC_API_DOMAIN}/${request.dashboard.name}`, request.dashboard, {
        params,
        withCredentials: true,
      })
      .pipe(map(normalizeDashboard));
  }

  /**
   * Deletes a dashboard by resource name.
   */
  deleteDashboard(name: string): Observable<void> {
    if (!name) {
      return throwError(() => new Error('Dashboard resource name is required for delete'));
    }
    return this.http.delete<void>(`${XMC_API_DOMAIN}/${name}`, {
      withCredentials: true,
    });
  }

  /**
   * Creates/provisions a chart under a dashboard parent or experiment.
   */
  createChart(request: CreateChartRequest): Observable<Chart> {
    if (!request.parent || !request.chart) {
      return throwError(() => new Error('Parent and chart resource are required'));
    }
    const paramsMap: Record<string, string> = {};
    if (request.chartId) {
      paramsMap['chartId'] = request.chartId;
    }
    const params = new HttpParams({ fromObject: paramsMap });
    return this.http
      .post<RawChart>(`${XMC_API_DOMAIN}/${request.parent}/charts`, request.chart, {
        params,
        withCredentials: true,
      })
      .pipe(map(normalizeChart));
  }

  /**
   * Fetches a chart definition by name.
   */
  getChart(name: string): Observable<Chart> {
    if (!name) {
      return throwError(() => new Error('Chart resource name is required'));
    }
    return this.http
      .get<RawChart>(`${XMC_API_DOMAIN}/${name}`, {
        withCredentials: true,
      })
      .pipe(map(normalizeChart));
  }

  /**
   * Core analytics API: fetches pre-processed metrics / plot data for a chart.
   */
  getChartData(request: GetChartDataRequest): Observable<ChartData> {
    if (!request.name) {
      return throwError(() => new Error('Chart resource name is required to fetch chart data'));
    }
    const paramsMap: Record<string, string | number> = {};
    if (request.parentExperiment) {
      paramsMap['parentExperiment'] = request.parentExperiment;
    }
    if (request.parentWorkunit) {
      paramsMap['parentWorkunit'] = request.parentWorkunit;
    }
    if (request.chartWidthPixels) {
      paramsMap['chartWidthPixels'] = request.chartWidthPixels;
    }

    const params = new HttpParams({ fromObject: paramsMap });
    return this.http
      .get<RawChartData>(`${XMC_API_DOMAIN}/${request.name}:getData`, {
        params,
        withCredentials: true,
      })
      .pipe(map((res) => normalizeChartData(res) ?? { plotsData: [] }));
  }
}
