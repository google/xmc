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

import 'jasmine';

import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { XMC_API_DOMAIN } from './constants';
import {
  Chart,
  ChartData,
  CreateChartRequest,
  CreateDashboardRequest,
  Dashboard,
  GetChartDataRequest,
  LayoutType,
  ListDashboardsRequest,
  ListDashboardsResponse,
  PlotType,
  RawChartData,
  UpdateDashboardRequest,
} from './dashboard_models';
import { DashboardService } from './dashboard_service';

describe('DashboardService', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), provideHttpClientTesting(), DashboardService],
    });

    return {
      service: get(DashboardService),
      httpMock: get(HttpTestingController),
    };
  }, beforeEach);

  afterEach(() => {
    state.httpMock.verify();
  });

  describe('getDashboard', () => {
    const mockDashboard: Dashboard = {
      name: 'dashboards/vcc-tune-comparison',
      title: 'VCC baseline tune comparison',
      owner: 'user@google.com',
      charts: [],
      chartLayout: { type: LayoutType.SIDE_BY_SIDE },
    };

    it('should fetch dashboard by name', async () => {
      const responsePromise = firstValueFrom(
        state.service.getDashboard('dashboards/vcc-tune-comparison'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/vcc-tune-comparison`);
      expect(req.request.method).toBe('GET');
      req.flush(mockDashboard);

      const response = await responsePromise;
      expect(response).toEqual(mockDashboard);
    });

    it('should return error when name is empty', async () => {
      try {
        await firstValueFrom(state.service.getDashboard(''));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Dashboard resource name is required');
      }
    });
  });

  describe('listDashboards', () => {
    const mockResponse: ListDashboardsResponse = {
      dashboards: [
        {
          name: 'dashboards/1',
          title: 'Dashboard 1',
          charts: [],
        },
      ],
      nextPageToken: 'token_2',
    };

    it('should list dashboards with query params', async () => {
      const request: ListDashboardsRequest = {
        pageSize: 10,
        pageToken: 'token_1',
        filter: 'owner:me',
      };
      const responsePromise = firstValueFrom(state.service.listDashboards(request));

      const req = state.httpMock.expectOne(
        `${XMC_API_DOMAIN}/dashboards?pageSize=10&pageToken=token_1&filter=owner:me`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response).toEqual(mockResponse);
    });

    it('should handle null/empty response gracefully without crashing', async () => {
      const responsePromise = firstValueFrom(state.service.listDashboards());

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards`);
      expect(req.request.method).toBe('GET');
      req.flush(null);

      const response = (await responsePromise) as ListDashboardsResponse;
      expect(response.dashboards).toEqual([]);
      expect(response.nextPageToken).toBeUndefined();
    });
  });

  describe('createDashboard', () => {
    const newDash: Dashboard = {
      title: 'New Comparison',
      charts: [],
    };

    it('should create dashboard via POST', async () => {
      const request: CreateDashboardRequest = {
        dashboard: newDash,
        dashboardId: 'custom-id',
      };
      const responsePromise = firstValueFrom(state.service.createDashboard(request));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards?dashboardId=custom-id`);
      expect(req.request.method).toBe('POST');
      expect(req.request.body).toEqual(newDash);
      req.flush({ ...newDash, name: 'dashboards/custom-id' });

      const response = (await responsePromise) as Dashboard;
      expect(response.name).toBe('dashboards/custom-id');
    });
  });

  describe('updateDashboard', () => {
    const updateDash: Dashboard = {
      name: 'dashboards/dash-1',
      title: 'Updated Title',
      charts: [],
    };

    it('should update dashboard via PATCH', async () => {
      const request: UpdateDashboardRequest = {
        dashboard: updateDash,
        updateMask: 'title',
      };
      const responsePromise = firstValueFrom(state.service.updateDashboard(request));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1?updateMask=title`);
      expect(req.request.method).toBe('PATCH');
      req.flush(updateDash);

      const response = await responsePromise;
      expect(response).toEqual(updateDash);
    });
  });

  describe('deleteDashboard', () => {
    it('should delete dashboard via DELETE', async () => {
      const responsePromise = firstValueFrom(state.service.deleteDashboard('dashboards/dash-1'));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1`);
      expect(req.request.method).toBe('DELETE');
      req.flush(null);

      await responsePromise;
    });
  });

  describe('createChart & getChart', () => {
    const mockChart: Chart = {
      name: 'dashboards/dash-1/charts/chart-1',
      title: 'Loss Chart',
      xid: 1001,
      wid: 1,
      plots: [
        {
          plotId: 'p1',
          type: PlotType.SUMMARY_BOX,
          data: { metrics: ['loss/train'] },
        },
      ],
    };

    it('should create chart under parent', async () => {
      const reqPayload: CreateChartRequest = {
        parent: 'dashboards/dash-1',
        chart: mockChart,
      };
      const responsePromise = firstValueFrom(state.service.createChart(reqPayload));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts`);
      expect(req.request.method).toBe('POST');
      req.flush(mockChart);

      const response = await responsePromise;
      expect(response).toEqual(mockChart);
    });

    it('should fetch chart by name', async () => {
      const responsePromise = firstValueFrom(
        state.service.getChart('dashboards/dash-1/charts/chart-1'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts/chart-1`);
      expect(req.request.method).toBe('GET');
      req.flush(mockChart);

      const response = await responsePromise;
      expect(response).toEqual(mockChart);
    });
  });

  describe('getChartData', () => {
    const mockRawChartData: RawChartData = {
      name: 'experiments/9001/charts/summary_single',
      xid: '9001',
      wid: '1',
      plots_data: [
        {
          plot_id: 'kpi_loss',
          type: PlotType.SUMMARY_BOX,
          content: {
            summary_data: {
              metric_name: 'train/loss',
              latest_value: 0.1631,
              mean_value: 0.7398,
            },
          },
        },
      ],
    };

    it('should fetch and normalize chart metrics data via custom :getData endpoint', async () => {
      const request: GetChartDataRequest = {
        name: 'experiments/9001/charts/summary_single',
        chartWidthPixels: 800,
      };
      const responsePromise = firstValueFrom<ChartData>(state.service.getChartData(request));

      const req = state.httpMock.expectOne(
        `${XMC_API_DOMAIN}/experiments/9001/charts/summary_single:getData?chartWidthPixels=800`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(mockRawChartData);

      const response = await responsePromise;
      expect(response.name).toBe('experiments/9001/charts/summary_single');
      expect(response.xid).toBe(9001);
      expect(response.wid).toBe(1);
      expect(response.plotsData?.[0].plotId).toBe('kpi_loss');
      expect(response.plotsData?.[0].summaryData?.latestValue).toBe(0.1631);
    });
  });
});
