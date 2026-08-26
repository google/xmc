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

import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter, Router } from '@angular/router';
import { of, throwError } from 'rxjs';

import { Chart, Dashboard } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { createMockDashboardList } from 'xmanager_cloud/ui/app/testing/mock_dashboards';
import {
  bootstrap,
  cleanState,
  flush,
  get,
  getEl,
  getEls,
  setupModule,
} from 'xmanager_cloud/ui/app/testing/test';

import { DashboardsListPage } from './dashboards_list_page';

const DASHBOARD_ROW_SELECTOR = '.dashboard-row';
const DASHBOARD_TITLE_TEXT_SELECTOR = '.dashboard-title-text';

/** Baseline dialog result: one chart to provision under a single work unit. */
function createDialogResult(charts: Chart[] = [{ title: 'Baseline', xid: 4321, wid: 1 }]) {
  return { dashboard: { title: 'New Evaluation' }, charts };
}

describe('DashboardsListPage', () => {
  const state = cleanState(() => {
    const dashboardServiceSpy = jasmine.createSpyObj<DashboardService>('DashboardService', [
      'listDashboards',
      'createDashboard',
      'createChart',
    ]);
    const snackbarServiceSpy = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);
    dashboardServiceSpy.listDashboards.and.returnValue(
      of({ dashboards: createMockDashboardList() }),
    );
    dashboardServiceSpy.createChart.and.returnValue(
      of({ name: 'experiments/4321/workUnits/1/charts/chart-uuid-1', title: 'Baseline' }),
    );

    setupModule({
      imports: [DashboardsListPage, NoopAnimationsModule],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        { provide: DashboardService, useValue: dashboardServiceSpy },
        { provide: SnackbarService, useValue: snackbarServiceSpy },
      ],
    });

    return { dashboardServiceSpy, snackbarServiceSpy };
  }, beforeEach);

  it('should create the component', () => {
    const component = bootstrap(DashboardsListPage);
    expect(component).toBeTruthy();
  });

  it('should load and display dashboards on init', () => {
    const component = bootstrap(DashboardsListPage);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalled();
    expect(component.dashboards().length).toBe(2);
    expect(component.filteredDashboards().length).toBe(2);
  });

  it('should request an explicit page size when loading dashboards', () => {
    bootstrap(DashboardsListPage);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({ pageSize: 100 });
  });

  it('should filter dashboards by search query', () => {
    const component = bootstrap(DashboardsListPage);
    component.searchQuery.set('vcc');
    expect(component.filteredDashboards().length).toBe(1);
    expect(component.filteredDashboards()[0].title).toBe('VCC baseline tune comparison');

    component.searchQuery.set('nonexistent');
    expect(component.filteredDashboards().length).toBe(0);

    component.searchQuery.set('');
    expect(component.filteredDashboards().length).toBe(2);
  });

  it('should render accessible aria-labels on the filter input and clear button', async () => {
    const component = bootstrap(DashboardsListPage);
    const filterInput = getEl('.search-field input');
    expect(filterInput?.getAttribute('aria-label')).toBe(
      'Filter dashboards by name or description',
    );

    component.searchQuery.set('vcc');
    await flush();
    const clearButton = getEl('.search-field button');
    expect(clearButton?.getAttribute('aria-label')).toBe('Clear search query');
  });

  it('should extract dashboard ID correctly', () => {
    const component = bootstrap(DashboardsListPage);
    expect(component.getDashboardId('dashboards/test_id')).toBe('test_id');
  });

  it('should render table rows in the DOM', () => {
    bootstrap(DashboardsListPage);
    const rows = getEls(DASHBOARD_ROW_SELECTOR);
    expect(rows.length).toBe(2);

    const firstTitle = getEl(DASHBOARD_TITLE_TEXT_SELECTOR);
    expect(firstTitle?.textContent?.trim()).toBe('VCC baseline tune comparison');
  });

  it('should create each chart under its work unit before creating the dashboard', () => {
    const component = bootstrap(DashboardsListPage);
    const createdDashboard: Dashboard = {
      name: 'dashboards/new_evaluation',
      title: 'New Evaluation',
    };
    state.dashboardServiceSpy.createDashboard.and.returnValue(of(createdDashboard));

    component.createDashboard(
      createDialogResult([
        { title: 'Baseline', xid: 4321, wid: 1 },
        { title: 'Tuned', xid: 8765, wid: 2 },
      ]),
    );

    expect(state.dashboardServiceSpy.createChart).toHaveBeenCalledTimes(2);
    expect(state.dashboardServiceSpy.createChart).toHaveBeenCalledWith({
      parent: 'experiments/4321/workUnits/1',
      chart: { title: 'Baseline', xid: 4321, wid: 1 },
    });
    expect(state.dashboardServiceSpy.createChart).toHaveBeenCalledWith({
      parent: 'experiments/8765/workUnits/2',
      chart: { title: 'Tuned', xid: 8765, wid: 2 },
    });
  });

  it('should link the server-assigned chart IDs onto the created dashboard', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.createChart.and.returnValues(
      of({ name: 'experiments/4321/workUnits/1/charts/chart-uuid-1', title: 'Baseline' }),
      of({ name: 'experiments/8765/workUnits/2/charts/chart-uuid-2', title: 'Tuned' }),
    );
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      of({ name: 'dashboards/new_evaluation', title: 'New Evaluation' }),
    );

    component.createDashboard(
      createDialogResult([
        { title: 'Baseline', xid: 4321, wid: 1 },
        { title: 'Tuned', xid: 8765, wid: 2 },
      ]),
    );

    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({
      dashboard: {
        title: 'New Evaluation',
        chartIds: ['chart-uuid-1', 'chart-uuid-2'],
      },
    });
    expect(router.navigate).toHaveBeenCalledWith(['/dashboards', 'new_evaluation']);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(1);
  });

  it('should create a dashboard with no chart IDs when the dialog returned no charts', () => {
    const component = bootstrap(DashboardsListPage);
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      of({ name: 'dashboards/empty', title: 'New Evaluation' }),
    );

    component.createDashboard(createDialogResult([]));

    expect(state.dashboardServiceSpy.createChart).not.toHaveBeenCalled();
    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({
      dashboard: { title: 'New Evaluation', chartIds: [] },
    });
  });

  it('should reload dashboards when createDashboard returns dashboard without id', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      of({ name: '', title: 'New Evaluation' }),
    );

    component.createDashboard(createDialogResult());

    expect(router.navigate).not.toHaveBeenCalled();
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(2);
  });

  it('should surface an error and skip the dashboard write when chart creation fails', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.createChart.and.returnValue(
      throwError(() => new Error('Chart creation failed')),
    );

    component.createDashboard(createDialogResult());

    expect(state.dashboardServiceSpy.createDashboard).not.toHaveBeenCalled();
    expect(router.navigate).not.toHaveBeenCalled();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to create dashboard');
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(2);
    expect(component.isLoading()).toBeFalse();
  });

  it('should reload dashboards when createDashboard fails', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      throwError(() => new Error('Creation failed')),
    );

    component.createDashboard(createDialogResult());

    expect(router.navigate).not.toHaveBeenCalled();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to create dashboard');
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(2);
    expect(component.isLoading()).toBeFalse();
  });

  it('should show error snackbar when listDashboards fails', () => {
    state.dashboardServiceSpy.listDashboards.and.returnValue(
      throwError(() => new Error('Server error')),
    );
    const component = bootstrap(DashboardsListPage);

    expect(component.isLoading()).toBeFalse();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to load dashboards');
  });
});
