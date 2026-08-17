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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { AddExperimentDialogComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/add_experiment_dialog/add_experiment_dialog';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { DashboardsPage } from './dashboards_page';
import { DashboardsPageHarness } from './dashboards_page_harness';

@Component({
  standalone: true,
  imports: [DashboardsPage],
  template: `<xmc-dashboards-page />`,
})
class TestHostComponent {}

describe('DashboardsPage', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let pageComponent: DashboardsPage;
  let harness: DashboardsPageHarness;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj('DashboardService', [
      'getDashboard',
      'getChartData',
    ]);
    dashboardServiceSpy.getChartData.and.returnValue(
      of({
        plotsData: [],
      }),
    );
    dashboardServiceSpy.getDashboard.and.returnValue(
      of({
        name: 'dashboards/vcc_comparison',
        title: 'VCC baseline tune comparison',
        charts: [
          {
            name: 'experiments/234911122/charts/baseline',
            title: 'VCC Baseline',
            xid: 234911122,
            plots: [],
          },
          {
            name: 'experiments/234911123/charts/tuned_v1',
            title: 'VCC tuned_v1',
            xid: 234911123,
            plots: [],
          },
          {
            name: 'experiments/234911124/charts/tuned_v2',
            title: 'VCC tuned_v2',
            xid: 234911124,
            plots: [],
          },
        ],
      }),
    );

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
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
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    pageComponent = fixture.debugElement.children[0].componentInstance as DashboardsPage;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(DashboardsPageHarness);
  });

  it('should render default dashboard title from Figma', async () => {
    expect(await harness.getTitle()).toBe('VCC baseline tune comparison');
  });

  it('should render initial comparative experiment columns', async () => {
    const grid = await harness.getGridHarness();
    expect(await grid.getChartCount()).toBe(3);
  });

  it('should open AddExperimentDialog when clicking add experiment', () => {
    const mockDialogRef = {
      afterClosed: () => of({ xid: 234911125, wid: 1, title: 'VCC tuned_v3' }),
    } as MatDialogRef<AddExperimentDialogComponent>;
    spyOn(MatDialog.prototype, 'open').and.returnValue(mockDialogRef);

    pageComponent.onOpenAddExperiment();
    expect(MatDialog.prototype.open).toHaveBeenCalled();
    expect(pageComponent.charts().length).toBe(4);
  });

  it('should remove chart from comparison', () => {
    expect(pageComponent.charts().length).toBe(3);
    pageComponent.onRemoveChart('experiments/234911124/charts/tuned_v2');
    expect(pageComponent.charts().length).toBe(2);
  });
});
