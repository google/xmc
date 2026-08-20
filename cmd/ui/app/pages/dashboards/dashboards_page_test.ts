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

import { Clipboard } from '@angular/cdk/clipboard';
import { TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';

import { AddExperimentDialogComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/add_experiment_dialog/add_experiment_dialog';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { createMockComparisonDashboard } from 'xmanager_cloud/ui/app/testing/mock_dashboards';
import { bootstrap, cleanState, getHarness, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { DashboardsPage } from './dashboards_page';
import { DashboardsPageHarness } from './dashboards_page_harness';

describe('DashboardsPage', () => {
  const state = cleanState(() => {
    const dashboardServiceSpy = jasmine.createSpyObj<DashboardService>('DashboardService', [
      'getDashboard',
      'getChartData',
      'updateDashboard',
    ]);
    const snackbarServiceSpy = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);

    dashboardServiceSpy.updateDashboard.and.returnValue(of({}));
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

  it('should open AddExperimentDialog when clicking add experiment and deep-copy template plots', () => {
    const pageComponent = bootstrap(DashboardsPage);
    const initialPlot = { plotId: 'val_loss_kpi', title: 'Test Plot' } as any;
    pageComponent.charts.set([
      {
        name: 'experiments/1/charts/1',
        title: 'Exp 1',
        xid: 1,
        plots: [initialPlot],
      },
    ]);

    const mockDialogRef = {
      afterClosed: () => of({ xid: 234911125, wid: 1, title: 'VCC tuned_v3' }),
    } as MatDialogRef<AddExperimentDialogComponent>;
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockDialogRef);

    pageComponent.onOpenAddExperiment();
    expect(MatDialog.prototype.open).toHaveBeenCalled();
    expect(pageComponent.charts().length).toBe(2);
    expect(pageComponent.charts()[1].plots).toEqual([initialPlot]);
    expect(pageComponent.charts()[1].plots).not.toBe(pageComponent.charts()[0].plots);
    expect(pageComponent.charts()[1].plots![0]).not.toBe(pageComponent.charts()[0].plots![0]);
  });

  it('should open AddExperimentDialog and use empty array for template plots when no charts exist', () => {
    const pageComponent = bootstrap(DashboardsPage);
    pageComponent.charts.set([]);

    const mockDialogRef = {
      afterClosed: () => of({ xid: 234911125, wid: 1, title: 'VCC tuned_v3' }),
    } as MatDialogRef<AddExperimentDialogComponent>;
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockDialogRef);

    pageComponent.onOpenAddExperiment();
    expect(MatDialog.prototype.open).toHaveBeenCalled();
    expect(pageComponent.charts().length).toBe(1);
    expect(pageComponent.charts()[0].plots).toEqual([]);
  });

  it('should show not-found state when dashboard fails to load', () => {
    state.dashboardServiceSpy.getDashboard.and.returnValue(
      throwError(() => new Error('Not found')),
    );
    const pageComponent = bootstrap(DashboardsPage);
    expect(pageComponent.dashboardNotFound()).toBeTrue();
    expect(pageComponent.isLoading()).toBeFalse();
  });

  it('should show not-found state when no dashboard ID is provided in route', () => {
    const route = TestBed.inject(ActivatedRoute);
    spyOn(route.snapshot.paramMap, 'get').and.returnValue(null);
    const pageComponent = bootstrap(DashboardsPage);
    expect(pageComponent.dashboardNotFound()).toBeTrue();
  });

  it('should remove chart from comparison', () => {
    const pageComponent = bootstrap(DashboardsPage);
    expect(pageComponent.charts().length).toBe(3);
    pageComponent.onRemoveChart('experiments/234911124/charts/tuned_v2');
    expect(pageComponent.charts().length).toBe(2);
  });

  describe('onShare', () => {
    it('should open success snackbar when clipboard copy succeeds', () => {
      const clipboard = TestBed.inject(Clipboard);
      spyOn(clipboard, 'copy').and.returnValue(true);
      const pageComponent = bootstrap(DashboardsPage);

      pageComponent.onShare();

      expect(clipboard.copy).toHaveBeenCalledWith(window.location.href);
      expect(state.snackbarServiceSpy.open).toHaveBeenCalledWith(
        'Dashboard link copied to clipboard',
      );
    });

    it('should open error snackbar when clipboard copy fails', () => {
      const clipboard = TestBed.inject(Clipboard);
      spyOn(clipboard, 'copy').and.returnValue(false);
      const pageComponent = bootstrap(DashboardsPage);

      pageComponent.onShare();

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

    pageComponent.onRemoveChart('experiments/234911124/charts/tuned_v2');
    expect(state.dashboardServiceSpy.updateDashboard).toHaveBeenCalled();
  });
});
