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
import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';

import {
  generateMockChartData,
  MOCK_DASHBOARD_1,
  MOCK_DASHBOARDS_LIST,
} from 'xmanager_cloud/ui/app/common/mock_data';

import { DashboardService } from '../dashboard_service';
import { ChartData, Dashboard, GetChartDataRequest } from '../dashboard_types';

/**
 * Mock implementation of DashboardService for local development and testing.
 */
@Injectable({ providedIn: 'root' })
export class MockDashboardService extends DashboardService {
  override listDashboards(): Observable<{ readonly dashboards: readonly Dashboard[] }> {
    return of({ dashboards: MOCK_DASHBOARDS_LIST });
  }

  override getDashboard(name: string): Observable<Dashboard> {
    const found = MOCK_DASHBOARDS_LIST.find((d) => d.name === name);
    return of(found ?? MOCK_DASHBOARD_1);
  }

  override createDashboard(dashboard: Dashboard): Observable<Dashboard> {
    return of(dashboard);
  }

  override getChartData(request: GetChartDataRequest): Observable<ChartData> {
    return of(generateMockChartData(request));
  }
}
