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

import { HttpErrorResponse, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import type {
  CreateChartRequest,
  CreateDashboardRequest,
  GetChartDataRequest,
  ListChartsRequest,
  ListChartsResponse,
  ListDashboardsRequest,
  ListDashboardsResponse,
  UpdateDashboardRequest,
} from 'xmanager_cloud/dashboard_service/proto/dashboard_service.jsonpb_decls';

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { XMC_API_DOMAIN } from './constants';
import {
  Chart,
  ChartData,
  Dashboard,
  LayoutType,
  PlotType,
  RawChartData,
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

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const responsePromise = firstValueFrom(
        state.service.getDashboard('dashboards/vcc-tune-comparison'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/vcc-tune-comparison`);
      req.flush('Not Found', { status: 404, statusText: 'Not Found' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(404);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to fetch dashboard dashboards/vcc-tune-comparison:',
          jasmine.any(Object),
        );
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

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const responsePromise = firstValueFrom(state.service.listDashboards());

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards`);
      req.flush('Internal Server Error', { status: 500, statusText: 'Internal Server Error' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(500);
        expect(consoleSpy).toHaveBeenCalledWith('Failed to list dashboards:', jasmine.any(Object));
      }
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

    it('should return error when dashboard payload is missing', async () => {
      try {
        await firstValueFrom(state.service.createDashboard({} as CreateDashboardRequest));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Dashboard payload is required');
      }
    });

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const request: CreateDashboardRequest = {
        dashboard: newDash,
      };
      const responsePromise = firstValueFrom(state.service.createDashboard(request));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards`);
      req.flush('Bad Request', { status: 400, statusText: 'Bad Request' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(400);
        expect(consoleSpy).toHaveBeenCalledWith('Failed to create dashboard:', jasmine.any(Object));
      }
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

    it('should return error when dashboard or dashboard.name is missing', async () => {
      try {
        await firstValueFrom(state.service.updateDashboard({} as UpdateDashboardRequest));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Dashboard and resource name are required');
      }
    });

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const request: UpdateDashboardRequest = {
        dashboard: updateDash,
      };
      const responsePromise = firstValueFrom(state.service.updateDashboard(request));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1`);
      req.flush('Forbidden', { status: 403, statusText: 'Forbidden' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(403);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to update dashboard dashboards/dash-1:',
          jasmine.any(Object),
        );
      }
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

    it('should return error when name is empty', async () => {
      try {
        await firstValueFrom(state.service.deleteDashboard(''));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Dashboard resource name is required for delete');
      }
    });

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const responsePromise = firstValueFrom(state.service.deleteDashboard('dashboards/dash-1'));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1`);
      req.flush('Internal Server Error', { status: 500, statusText: 'Internal Server Error' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(500);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to delete dashboard dashboards/dash-1:',
          jasmine.any(Object),
        );
      }
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

    it('should return error when parent or chart is missing', async () => {
      try {
        await firstValueFrom(state.service.createChart({} as CreateChartRequest));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Parent and chart resource are required');
      }
    });

    it('should log structured diagnostic and rethrow error when createChart HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const reqPayload: CreateChartRequest = {
        parent: 'dashboards/dash-1',
        chart: mockChart,
      };
      const responsePromise = firstValueFrom(state.service.createChart(reqPayload));

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts`);
      req.flush('Bad Request', { status: 400, statusText: 'Bad Request' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(400);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to create chart under parent dashboards/dash-1:',
          jasmine.any(Object),
        );
      }
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

    it('should return error when chart name is empty', async () => {
      try {
        await firstValueFrom(state.service.getChart(''));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Chart resource name is required');
      }
    });

    it('should log structured diagnostic and rethrow error when getChart HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const responsePromise = firstValueFrom(
        state.service.getChart('dashboards/dash-1/charts/chart-1'),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts/chart-1`);
      req.flush('Not Found', { status: 404, statusText: 'Not Found' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(404);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to fetch chart dashboards/dash-1/charts/chart-1:',
          jasmine.any(Object),
        );
      }
    });
  });

  describe('listCharts', () => {
    const serverResponse = {
      charts: [
        {
          name: 'charts/chart-1',
          title: 'Loss Chart',
        },
      ],
      nextPageToken: 'token_2',
    };
    // normalizeChart backfills the required xid and plots fields.
    const expectedResponse: ListChartsResponse = {
      charts: [{ name: 'charts/chart-1', title: 'Loss Chart', xid: 0, plots: [] }],
      nextPageToken: 'token_2',
    };

    it('should list charts under a dashboard parent with query params', async () => {
      const request: ListChartsRequest = {
        parent: 'dashboards/dash-1',
        pageSize: 10,
        pageToken: 'token_1',
      };
      const responsePromise = firstValueFrom(state.service.listCharts(request));

      const req = state.httpMock.expectOne(
        `${XMC_API_DOMAIN}/dashboards/dash-1/charts?pageSize=10&pageToken=token_1`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(serverResponse);

      const response = await responsePromise;
      expect(response).toEqual(expectedResponse);
    });

    it('should list charts under a work unit parent', async () => {
      const responsePromise = firstValueFrom(
        state.service.listCharts({ parent: 'experiments/42/workUnits/7' }),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/experiments/42/workUnits/7/charts`);
      expect(req.request.method).toBe('GET');
      req.flush(serverResponse);

      const response = await responsePromise;
      expect(response).toEqual(expectedResponse);
    });

    it('should return error when parent is missing', async () => {
      try {
        await firstValueFrom(state.service.listCharts({} as ListChartsRequest));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain('Parent resource name is required');
      }
    });

    it('should handle null/empty response gracefully without crashing', async () => {
      const responsePromise = firstValueFrom(
        state.service.listCharts({ parent: 'dashboards/dash-1' }),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts`);
      expect(req.request.method).toBe('GET');
      req.flush(null);

      const response = (await responsePromise) as ListChartsResponse;
      expect(response.charts).toEqual([]);
      expect(response.nextPageToken).toBeUndefined();
    });

    it('should log structured diagnostic and rethrow error when HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const responsePromise = firstValueFrom(
        state.service.listCharts({ parent: 'dashboards/dash-1' }),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/dashboards/dash-1/charts`);
      req.flush('Internal Server Error', { status: 500, statusText: 'Internal Server Error' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(500);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to list charts under parent dashboards/dash-1:',
          jasmine.any(Object),
        );
      }
    });
  });

  describe('getChartData', () => {
    const mockRawChartData: RawChartData = {
      name: 'experiments/9001/workUnits/1/charts/summary_single',
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
        name: 'experiments/9001/workUnits/1/charts/summary_single',
        chartWidthPixels: 800,
      };
      const responsePromise = firstValueFrom<ChartData>(state.service.getChartData(request));

      const req = state.httpMock.expectOne(
        `${XMC_API_DOMAIN}/experiments/9001/workUnits/1/charts/summary_single:getData?chartWidthPixels=800`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(mockRawChartData);

      const response = await responsePromise;
      expect(response.name).toBe('experiments/9001/workUnits/1/charts/summary_single');
      expect(response.xid).toBe(9001);
      expect(response.wid).toBe(1);
      expect(response.plotsData?.[0].plotId).toBe('kpi_loss');
      expect(response.plotsData?.[0].summaryData?.latestValue).toBe(0.1631);
    });

    it('should return error when chart name is empty', async () => {
      try {
        await firstValueFrom(state.service.getChartData({} as GetChartDataRequest));
        fail('expected error');
      } catch (err: unknown) {
        expect((err as Error).message).toContain(
          'Chart resource name is required to fetch chart data',
        );
      }
    });

    it('should log structured diagnostic and rethrow error when getChartData HTTP request fails', async () => {
      const consoleSpy = spyOn(console, 'error');
      const request: GetChartDataRequest = {
        name: 'experiments/9001/workUnits/1/charts/summary_single',
      };
      const responsePromise = firstValueFrom<ChartData>(state.service.getChartData(request));

      const req = state.httpMock.expectOne(
        `${XMC_API_DOMAIN}/experiments/9001/workUnits/1/charts/summary_single:getData`,
      );
      req.flush('Gateway Timeout', { status: 504, statusText: 'Gateway Timeout' });

      try {
        await responsePromise;
        fail('expected error');
      } catch (err: unknown) {
        expect((err as HttpErrorResponse).status).toBe(504);
        expect(consoleSpy).toHaveBeenCalledWith(
          'Failed to fetch chart data for experiments/9001/workUnits/1/charts/summary_single:',
          jasmine.any(Object),
        );
      }
    });
  });
});
