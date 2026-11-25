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

import { Clipboard } from '@angular/cdk/clipboard';
import { TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';

import { AddExperimentDialogComponent } from 'app/common/components/dashboard/add_experiment_dialog/add_experiment_dialog';
import { DashboardGridComponent } from 'app/common/components/dashboard/dashboard_grid/dashboard_grid';
import { PlotSpec } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';
import { SnackbarService } from 'app/service/snackbar_service';
import {
  createMockComparisonCharts,
  createMockComparisonDashboard,
} from 'app/testing/mock_dashboards';
import { bootstrap, cleanState, getDebugEl, getHarness, setupModule } from 'app/testing/test';

import { DashboardsPage } from './dashboards_page';
import { DashboardsPageHarness } from './dashboards_page_harness';

/** Dialog ref stub that closes with a single comparison experiment. */
function mockAddExperimentDialogRef(): MatDialogRef<AddExperimentDialogComponent> {
  return {
    afterClosed: () => of({ xid: 234911125, wid: 1, title: 'VCC tuned_v3' }),
  } as MatDialogRef<AddExperimentDialogComponent>;
}

describe('DashboardsPage', () => {
  const state = cleanState(() => {
    const dashboardServiceSpy = jasmine.createSpyObj<DashboardService>('DashboardService', [
      'getDashboard',
      'getChartData',
      'listCharts',
      'updateDashboard',
      'createChart',
    ]);
    const snackbarServiceSpy = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);

    dashboardServiceSpy.updateDashboard.and.returnValue(of({}));
    dashboardServiceSpy.createChart.and.returnValue(
      of({
        name: 'experiments/234911125/workUnits/1/charts/tuned_v3',
        title: 'VCC tuned_v3',
        xid: 234911125,
        wid: 1,
      }),
    );
    dashboardServiceSpy.listCharts.and.returnValue(of({ charts: createMockComparisonCharts() }));
    dashboardServiceSpy.getChartData.and.returnValue(
      of({
        plotsData: [],
      }),
    );
    dashboardServiceSpy.getDashboard.and.returnValue(of(createMockComparisonDashboard()));

    setupModule({
      imports: [DashboardsPage, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: {
              paramMap: {
                get: (key: string) => (key === 'id' ? 'vcc_comparison' : null),
              },
            },
          },
        },
        { provide: DashboardService, useValue: dashboardServiceSpy },
        { provide: SnackbarService, useValue: snackbarServiceSpy },
      ],
    });

    return { dashboardServiceSpy, snackbarServiceSpy };
  }, beforeEach);

  it('should render default dashboard title from Figma', async () => {
    bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    expect(await harness.getTitle()).toBe('VCC baseline tune comparison');
  });

  it('should render initial comparative experiment columns', async () => {
    bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    const grid = await harness.getGridHarness();
    expect(await grid.getChartCount()).toBe(3);
  });

  it('should open AddExperimentDialog when clicking add experiment and deep-copy template plots', async () => {
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    const initialPlot = { plotId: 'val_loss_kpi', title: 'Test Plot' } as PlotSpec;
    pageComponent.charts.set([
      {
        name: 'experiments/1/charts/1',
        title: 'Exp 1',
        xid: 1,
        plots: [initialPlot],
      },
    ]);
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockAddExperimentDialogRef());

    await harness.clickAddExperiment();

    expect(MatDialog.prototype.open).toHaveBeenCalled();
    const requested = state.dashboardServiceSpy.createChart.calls.mostRecent().args[0].chart;
    expect(requested.plots).toEqual([initialPlot]);
    expect(requested.plots).not.toBe(pageComponent.charts()[0].plots);
    expect(requested.plots[0]).not.toBe(initialPlot);
  });

  it('should open AddExperimentDialog and use empty array for template plots when no charts exist', async () => {
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    pageComponent.charts.set([]);
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockAddExperimentDialogRef());

    await harness.clickAddExperiment();

    expect(MatDialog.prototype.open).toHaveBeenCalled();
    expect(state.dashboardServiceSpy.createChart.calls.mostRecent().args[0].chart.plots).toEqual(
      [],
    );
  });

  it('should persist the comparison chart under its work unit and append the server copy', async () => {
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockAddExperimentDialogRef());

    await harness.clickAddExperiment();

    expect(state.dashboardServiceSpy.createChart).toHaveBeenCalledWith({
      parent: 'experiments/234911125/workUnits/1',
      chart: jasmine.objectContaining({ title: 'VCC tuned_v3', xid: 234911125, wid: 1 }),
    });
    expect(pageComponent.charts().length).toBe(4);
    expect(pageComponent.charts()[3].name).toBe(
      'experiments/234911125/workUnits/1/charts/tuned_v3',
    );
    expect(state.dashboardServiceSpy.updateDashboard).toHaveBeenCalledWith({
      updateMask: 'chart_ids',
      dashboard: {
        name: 'dashboards/vcc_comparison',
        chartIds: ['baseline', 'tuned_v1', 'tuned_v2', 'tuned_v3'],
      },
    });
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith(
      'Added experiment 234911125 to comparison',
    );
  });

  it('should leave the dashboard untouched when the comparison chart cannot be created', async () => {
    state.dashboardServiceSpy.createChart.and.returnValue(throwError(() => new Error('boom')));
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockAddExperimentDialogRef());

    await harness.clickAddExperiment();

    expect(pageComponent.charts().length).toBe(3);
    expect(state.dashboardServiceSpy.updateDashboard).not.toHaveBeenCalled();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith(
      'Failed to add experiment 234911125',
    );
  });

  it('should not modify charts when AddExperimentDialog is cancelled', () => {
    const pageComponent = bootstrap(DashboardsPage);
    const mockDialogRef = {
      afterClosed: () => of(undefined),
    } as MatDialogRef<AddExperimentDialogComponent>;
    spyOn(TestBed.inject(MatDialog), 'open').and.returnValue(mockDialogRef);

    pageComponent.onOpenAddExperiment();
    expect(pageComponent.charts().length).toBe(3);
  });

  it('should load chart bodies through ListCharts on initial load', () => {
    const pageComponent = bootstrap(DashboardsPage);
    expect(state.dashboardServiceSpy.listCharts).toHaveBeenCalledWith({
      parent: 'dashboards/vcc_comparison',
    });
    expect(pageComponent.charts().length).toBe(3);
    expect(pageComponent.isLoading()).toBeFalse();
  });

  it('should render an empty dashboard and warn when the initial chart load fails', () => {
    state.dashboardServiceSpy.listCharts.and.returnValue(throwError(() => new Error('boom')));
    const pageComponent = bootstrap(DashboardsPage);
    expect(pageComponent.charts().length).toBe(0);
    expect(pageComponent.isLoading()).toBeFalse();
    expect(pageComponent.dashboardNotFound()).toBeFalse();
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to load dashboard charts');
  });

  it('should refresh dashboard charts when clicking refresh button', async () => {
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    state.dashboardServiceSpy.listCharts.and.returnValue(
      of({ charts: [{ name: 'charts/refreshed', title: 'Refreshed Chart' }] }),
    );
    await harness.clickRefresh();
    expect(state.dashboardServiceSpy.listCharts).toHaveBeenCalledWith({
      parent: 'dashboards/vcc_comparison',
    });
    expect(pageComponent.charts().length).toBe(1);
    expect(pageComponent.charts()[0].name).toBe('charts/refreshed');
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Dashboard refreshed');
  });

  it('should keep existing charts and warn when refresh fails', async () => {
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    state.dashboardServiceSpy.listCharts.and.returnValue(throwError(() => new Error('boom')));
    await harness.clickRefresh();
    expect(pageComponent.charts().length).toBe(3);
    expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith('Failed to refresh dashboard');
  });

  it('should show not-found state when dashboard fails to load', async () => {
    state.dashboardServiceSpy.getDashboard.and.returnValue(
      throwError(() => new Error('Not found')),
    );
    const pageComponent = bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    expect(await harness.isNotFoundVisible()).toBeTrue();
    expect(pageComponent.isLoading()).toBeFalse();
  });

  it('should show not-found state when no dashboard ID is provided in route', async () => {
    const route = TestBed.inject(ActivatedRoute);
    spyOn(route.snapshot.paramMap, 'get').and.returnValue(null);
    bootstrap(DashboardsPage);
    const harness = await getHarness(DashboardsPageHarness);
    expect(await harness.isNotFoundVisible()).toBeTrue();
  });

  it('should display not-found state when dashboard ID does not exist', () => {
    state.dashboardServiceSpy.getDashboard.and.returnValue(
      throwError(() => new Error('Not found')),
    );
    const comp = bootstrap(DashboardsPage);
    comp.loadDashboard('dashboards/unknown_id');
    expect(comp.dashboardNotFound()).toBeTrue();
  });

  it('should remove chart from comparison', () => {
    const pageComponent = bootstrap(DashboardsPage);
    expect(pageComponent.charts().length).toBe(3);
    const grid = getDebugEl(DashboardGridComponent).componentInstance as DashboardGridComponent;
    grid.removeChart.emit('experiments/234911124/charts/tuned_v2');
    expect(pageComponent.charts().length).toBe(2);
  });

  describe('onShare', () => {
    it('should open success snackbar when clipboard copy succeeds', async () => {
      const clipboard = TestBed.inject(Clipboard);
      spyOn(clipboard, 'copy').and.returnValue(true);
      bootstrap(DashboardsPage);
      const harness = await getHarness(DashboardsPageHarness);

      await harness.clickShare();

      expect(clipboard.copy).toHaveBeenCalledWith(window.location.href);
      expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith(
        'Dashboard link copied to clipboard',
      );
    });

    it('should open error snackbar when clipboard copy fails', async () => {
      const clipboard = TestBed.inject(Clipboard);
      spyOn(clipboard, 'copy').and.returnValue(false);
      bootstrap(DashboardsPage);
      const harness = await getHarness(DashboardsPageHarness);

      await harness.clickShare();

      expect(clipboard.copy).toHaveBeenCalledWith(window.location.href);
      expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith(
        'Failed to copy link to clipboard',
      );
    });
  });

  it('should persist dashboard update when removing a chart and autosave is enabled', () => {
    const pageComponent = bootstrap(DashboardsPage);
    pageComponent.loadDashboard('dashboards/vcc_comparison');
    state.dashboardServiceSpy.updateDashboard.calls.reset();

    const grid = getDebugEl(DashboardGridComponent).componentInstance as DashboardGridComponent;
    grid.removeChart.emit('experiments/234911124/charts/tuned_v2');
    expect(state.dashboardServiceSpy.updateDashboard).toHaveBeenCalledWith({
      updateMask: 'chart_ids',
      dashboard: {
        name: 'dashboards/vcc_comparison',
        chartIds: ['baseline', 'tuned_v1'],
      },
    });
  });
});
