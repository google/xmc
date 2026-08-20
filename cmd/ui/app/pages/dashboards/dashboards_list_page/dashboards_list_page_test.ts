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
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';

import { Dashboard } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { DashboardsListPage, extractDashboardId } from './dashboards_list_page';

describe('DashboardsListPage', () => {
  let component: DashboardsListPage;
  let fixture: ComponentFixture<DashboardsListPage>;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  const mockDashboards: Dashboard[] = [
    {
      name: 'dashboards/vcc_comparison',
      title: 'VCC baseline tune comparison',
      description: 'Comparative evaluation of VCC baseline tuning across hyperparameter runs',
      charts: [
        { name: 'experiments/1/charts/c1', title: 'Chart 1', xid: 1, plots: [] },
        { name: 'experiments/2/charts/c2', title: 'Chart 2', xid: 2, plots: [] },
      ],
    },
    {
      name: 'dashboards/testdata_overview',
      title: 'Data Engine Golden Testcases',
      description: 'Overview of all testcases',
      charts: [],
    },
  ];

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj<DashboardService>('DashboardService', [
      'listDashboards',
    ]);
    dashboardServiceSpy.listDashboards.and.returnValue(of({ dashboards: mockDashboards }));

    await TestBed.configureTestingModule({
      imports: [DashboardsListPage],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        provideNoopAnimations(),
        { provide: DashboardService, useValue: dashboardServiceSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(DashboardsListPage);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create the component', () => {
    expect(component).toBeTruthy();
  });

  it('should load and display dashboards on init', () => {
    expect(dashboardServiceSpy.listDashboards).toHaveBeenCalled();
    expect(component.dashboards().length).toBe(2);
    expect(component.filteredDashboards().length).toBe(2);
  });

  it('should filter dashboards by search query', () => {
    component.searchQuery.set('vcc');
    expect(component.filteredDashboards().length).toBe(1);
    expect(component.filteredDashboards()[0].title).toBe('VCC baseline tune comparison');

    component.searchQuery.set('nonexistent');
    expect(component.filteredDashboards().length).toBe(0);

    component.searchQuery.set('');
    expect(component.filteredDashboards().length).toBe(2);
  });

  it('should extract dashboard ID correctly', () => {
    expect(extractDashboardId('dashboards/vcc_comparison')).toBe('vcc_comparison');
    expect(extractDashboardId('my_dashboard')).toBe('my_dashboard');
    expect(extractDashboardId('')).toBe('');
    expect(component.getDashboardId('dashboards/test_id')).toBe('test_id');
  });

  it('should render table rows in the DOM', () => {
    const compiled = fixture.nativeElement as HTMLElement;
    const rows = compiled.querySelectorAll('.dashboard-row');
    expect(rows.length).toBe(2);

    const firstTitle = compiled.querySelector('.dashboard-title-text');
    expect(firstTitle?.textContent?.trim()).toBe('VCC baseline tune comparison');
  });

  it('should call createDashboard when dialog returns a valid dashboard', () => {
    const newDash: Dashboard = {
      name: 'dashboards/new_evaluation',
      title: 'New Evaluation',
      charts: [],
    };
    dashboardServiceSpy.createDashboard = jasmine
      .createSpy('createDashboard')
      .and.returnValue(of(newDash));

    component.createDashboard(newDash);

    expect(dashboardServiceSpy.createDashboard).toHaveBeenCalledWith({ dashboard: newDash });
  });

  it('should show error snackbar when listDashboards fails', () => {
    const snackbarOpenSpy = jasmine.createSpy('open');
    (component as unknown as { snackbar: { open: jasmine.Spy } }).snackbar = {
      open: snackbarOpenSpy,
    };
    dashboardServiceSpy.listDashboards.and.returnValue(throwError(() => new Error('Server error')));

    component.loadDashboards();

    expect(component.isLoading()).toBeFalse();
    expect(snackbarOpenSpy).toHaveBeenCalledWith('Failed to load dashboards');
  });
});
