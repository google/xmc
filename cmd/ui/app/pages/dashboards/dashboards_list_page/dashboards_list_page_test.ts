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
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';

import { Dashboard } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { createMockDashboardList } from 'xmanager_cloud/ui/app/testing/mock_dashboards';
import {
  bootstrap,
  cleanState,
  getEl,
  getEls,
  setupModule,
} from 'xmanager_cloud/ui/app/testing/test';

import { DashboardsListPage, extractDashboardId } from './dashboards_list_page';

const DASHBOARD_ROW_SELECTOR = '.dashboard-row';
const DASHBOARD_TITLE_TEXT_SELECTOR = '.dashboard-title-text';

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

  it('should load and display dashboards on init', () => {
    const component = bootstrap(DashboardsListPage);
    expect(state.dashboardServiceSpy.listDashboards).toHaveBeenCalled();
    expect(component.dashboards().length).toBe(2);
    expect(component.filteredDashboards().length).toBe(2);
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

  it('should extract dashboard ID correctly', () => {
    const component = bootstrap(DashboardsListPage);
    expect(extractDashboardId('dashboards/vcc_comparison')).toBe('vcc_comparison');
    expect(extractDashboardId('my_dashboard')).toBe('my_dashboard');
    expect(extractDashboardId('')).toBe('');
    expect(component.getDashboardId('dashboards/test_id')).toBe('test_id');
  });

  it('should render table rows in the DOM', () => {
    bootstrap(DashboardsListPage);
    const rows = getEls(DASHBOARD_ROW_SELECTOR);
    expect(rows.length).toBe(2);

    const firstTitle = getEl(DASHBOARD_TITLE_TEXT_SELECTOR);
    expect(firstTitle?.textContent?.trim()).toBe('VCC baseline tune comparison');
  });

  it('should call createDashboard when dialog returns a valid dashboard', () => {
    const component = bootstrap(DashboardsListPage);
    const newDash: Dashboard = {
      name: 'dashboards/new_evaluation',
      title: 'New Evaluation',
      charts: [],
    };
    state.dashboardServiceSpy.createDashboard.and.returnValue(of(newDash));

    component.createDashboard(newDash);

    expect(state.dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({ dashboard: newDash });
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
