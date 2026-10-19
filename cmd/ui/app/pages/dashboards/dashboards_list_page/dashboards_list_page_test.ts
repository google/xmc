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

import { Chart, Dashboard } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';
import { SnackbarService } from 'app/service/snackbar_service';
import { createMockDashboardList } from 'app/testing/mock_dashboards';
import {
  bootstrap,
  cleanState,
  flush,
  get,
  getEl,
  getEls,
  getHarness,
  setupModule,
} from 'app/testing/test';

import { DashboardsListPage } from './dashboards_list_page';
import { DashboardsListPageHarness } from './dashboards_list_page_harness';

const DASHBOARD_ROW_SELECTOR = '.dashboard-row';
const DASHBOARD_TITLE_TEXT_SELECTOR = '.dashboard-title-text';
const SEARCH_FIELD_INPUT_SELECTOR = '.search-field input';
const SEARCH_FIELD_CLEAR_BTN_SELECTOR = '.search-field button';

/** Baseline dialog result: one chart to create alongside the dashboard. */
function createDialogResult(charts: Chart[] = [{ title: 'Baseline', xid: 4321, wid: 1 }]) {
  return { dashboard: { title: 'New Evaluation' }, charts };
}

describe('DashboardsListPage', () => {
  const state = cleanState(() => {
    const dashboardServiceSpy = jasmine.createSpyObj<DashboardService>('DashboardService', [
      'listDashboards',
      'createDashboard',
    ]);
    const snackbarServiceSpy = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);
    dashboardServiceSpy.listDashboards.and.returnValue(
      of({ dashboards: createMockDashboardList() }),
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

  it('should load and display dashboards on init with default page size', () => {
    const component = bootstrap(DashboardsListPage);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({
      pageSize: 10,
      pageToken: undefined,
    });
    expect(component.dashboards().length).toBe(2);
    expect(component.filteredDashboards().length).toBe(2);
    expect(component.pageIndex()).toBe(0);
    expect(component.pageSize()).toBe(10);
    expect(component.hasNextPage()).toBeFalse();
    expect(component.hasPreviousPage()).toBeFalse();
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
    const filterInput = getEl(SEARCH_FIELD_INPUT_SELECTOR);
    expect(filterInput?.getAttribute('aria-label')).toBe(
      'Filter dashboards by name or description',
    );

    component.searchQuery.set('vcc');
    await flush();

    const clearButton = getEl(SEARCH_FIELD_CLEAR_BTN_SELECTOR);
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

  it('should handle pagination next and previous controls via harness', async () => {
    state.dashboardServiceSpy.listDashboards.and.returnValues(
      of({ dashboards: createMockDashboardList(), nextPageToken: 'token_page_2' }),
      of({ dashboards: createMockDashboardList() }),
      of({ dashboards: createMockDashboardList() }),
    );

    const component = bootstrap(DashboardsListPage);
    const harness = await getHarness(DashboardsListPageHarness);

    expect(await harness.getPageLabelText()).toContain('Page 1');
    expect(await harness.isPreviousPageDisabled()).toBeTrue();
    expect(await harness.isNextPageDisabled()).toBeFalse();

    await harness.clickNextPage();

    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({
      pageSize: 10,
      pageToken: 'token_page_2',
    });
    expect(component.pageIndex()).toBe(1);
    expect(await harness.getPageLabelText()).toContain('Page 2');
    expect(await harness.isPreviousPageDisabled()).toBeFalse();

    await harness.clickPreviousPage();

    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({
      pageSize: 10,
      pageToken: undefined,
    });
    expect(component.pageIndex()).toBe(0);
    expect(await harness.getPageLabelText()).toContain('Page 1');
    expect(await harness.isPreviousPageDisabled()).toBeTrue();
  });

  it('should update page size and reload first page', () => {
    const component = bootstrap(DashboardsListPage);
    state.dashboardServiceSpy.listDashboards.calls.reset();

    component.onPageSizeChange(25);

    expect(component.pageSize()).toBe(25);
    expect(component.pageIndex()).toBe(0);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({
      pageSize: 25,
      pageToken: undefined,
    });
  });

  it('should not navigate to next page if hasNextPage is false or loading', () => {
    const component = bootstrap(DashboardsListPage);
    state.dashboardServiceSpy.listDashboards.calls.reset();

    component.goToNextPage();
    expect(state.dashboardServiceSpy.listDashboards).not.toHaveBeenCalled();

    component.pagination.set({
      ...component.pagination(),
      nextPageToken: 'token_page_2',
    });
    component.isLoading.set(true);
    component.goToNextPage();
    expect(state.dashboardServiceSpy.listDashboards).not.toHaveBeenCalled();
  });

  it('should not navigate to previous page if hasPreviousPage is false or loading', () => {
    const component = bootstrap(DashboardsListPage);
    state.dashboardServiceSpy.listDashboards.calls.reset();

    component.goToPreviousPage();
    expect(state.dashboardServiceSpy.listDashboards).not.toHaveBeenCalled();

    component.pagination.set({
      ...component.pagination(),
      pageIndex: 1,
      previousTokens: [''],
    });
    component.isLoading.set(true);
    component.goToPreviousPage();
    expect(state.dashboardServiceSpy.listDashboards).not.toHaveBeenCalled();
  });

  it('should reset pagination to page 1 on refresh', async () => {
    state.dashboardServiceSpy.listDashboards.and.returnValues(
      of({ dashboards: createMockDashboardList(), nextPageToken: 'token_page_2' }),
      of({ dashboards: createMockDashboardList() }),
      of({ dashboards: createMockDashboardList() }),
    );

    const component = bootstrap(DashboardsListPage);
    const harness = await getHarness(DashboardsListPageHarness);

    await harness.clickNextPage();
    expect(component.pageIndex()).toBe(1);

    await harness.clickRefresh();
    expect(component.pageIndex()).toBe(0);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledWith({
      pageSize: 10,
      pageToken: undefined,
    });
  });

  it('should send the dialog charts alongside the dashboard in a single create call', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    const createdDashboard: Dashboard = {
      name: 'dashboards/new_evaluation',
      title: 'New Evaluation',
      chartIds: ['chart-uuid-1', 'chart-uuid-2'],
    };
    state.dashboardServiceSpy.createDashboard.and.returnValue(of(createdDashboard));

    component.createDashboard(
      createDialogResult([
        { title: 'Baseline', xid: 4321, wid: 1 },
        { title: 'Tuned', xid: 8765, wid: 2 },
      ]),
    );

    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledTimes(1);
    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({
      dashboard: { title: 'New Evaluation' },
      charts: [
        { title: 'Baseline', xid: 4321, wid: 1 },
        { title: 'Tuned', xid: 8765, wid: 2 },
      ],
    });
    expect(router.navigate).toHaveBeenCalledWith(['/dashboards', 'new_evaluation']);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(1);
  });

  it('should create a dashboard with no charts when the dialog returned none', () => {
    const component = bootstrap(DashboardsListPage);
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      of({ name: 'dashboards/empty', title: 'New Evaluation' }),
    );

    component.createDashboard(createDialogResult([]));

    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({
      dashboard: { title: 'New Evaluation' },
      charts: [],
    });
  });

  it('should reset pagination and reload dashboards when createDashboard returns dashboard without id', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      of({ name: '', title: 'New Evaluation' }),
    );

    component.createDashboard(createDialogResult());

    expect(router.navigate).not.toHaveBeenCalled();
    expect(component.pageIndex()).toBe(0);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalledTimes(2);
  });

  it('should not reload dashboards or reset pagination when createDashboard fails', () => {
    const component = bootstrap(DashboardsListPage);
    const router = get(Router);
    spyOn(router, 'navigate');
    state.dashboardServiceSpy.listDashboards.calls.reset();
    component.pagination.set({
      ...component.pagination(),
      pageIndex: 2,
      previousTokens: ['', 'token_page_2'],
    });
    state.dashboardServiceSpy.createDashboard.and.returnValue(
      throwError(() => new Error('Creation failed')),
    );

    component.createDashboard(createDialogResult());

    expect(router.navigate).not.toHaveBeenCalled();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to create dashboard');
    expect(component.pageIndex()).toBe(2);
    expect(state.dashboardServiceSpy.listDashboards).not.toHaveBeenCalled();
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
