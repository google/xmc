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
import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Observable } from 'rxjs';

import { ChartData, Dashboard, GetChartDataRequest } from './dashboard_types';

/**
 * Service for interacting with the XMC Dashboard Service.
 */
@Injectable({ providedIn: 'root' })
export class DashboardService {
  private readonly http = inject(HttpClient);
  private readonly baseUrl = '/api/v1';

  /** Lists available dashboards. */
  listDashboards(): Observable<{ readonly dashboards: readonly Dashboard[] }> {
    return this.http.get<{ readonly dashboards: readonly Dashboard[] }>(
      `${this.baseUrl}/dashboards`,
    );
  }

  /** Retrieves a single dashboard by resource name. */
  getDashboard(name: string): Observable<Dashboard> {
    return this.http.get<Dashboard>(`${this.baseUrl}/${name}`);
  }

  /** Creates a new dashboard resource. */
  createDashboard(dashboard: Dashboard): Observable<Dashboard> {
    return this.http.post<Dashboard>(`${this.baseUrl}/dashboards`, dashboard);
  }

  /** Fetches analytics metric data for a specific Experiment Chart. */
  getChartData(request: GetChartDataRequest): Observable<ChartData> {
    return this.http.post<ChartData>(`${this.baseUrl}/${request.name}:getChartData`, request);
  }
}
