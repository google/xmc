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
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { Chart, LayoutType, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { DashboardGridComponent } from './dashboard_grid';
import { DashboardGridHarness } from './dashboard_grid_harness';

@Component({
  standalone: true,
  imports: [DashboardGridComponent],
  template: ` <xmc-dashboard-grid [charts]="charts()" /> `,
})
class TestHostComponent {
  readonly charts = signal<Chart[]>([
    {
      name: 'experiments/234911122/charts/baseline',
      title: 'VCC Baseline',
      xid: 234911122,
      wid: 1,
      plotLayout: { type: LayoutType.GRID },
      plots: [
        {
          plotId: 'loss_kpi',
          title: 'Avg validation loss',
          type: PlotType.SUMMARY_BOX,
          data: { metrics: ['val/loss'] },
        },
      ],
    },
    {
      name: 'experiments/234911123/charts/tune1',
      title: 'VCC tuned_v1',
      xid: 234911123,
      wid: 1,
      plotLayout: { type: LayoutType.GRID },
      plots: [
        {
          plotId: 'loss_kpi',
          title: 'Avg validation loss',
          type: PlotType.SUMMARY_BOX,
          data: { metrics: ['val/loss'] },
        },
      ],
    },
    {
      name: 'experiments/234911124/charts/tune2',
      title: 'VCC tuned_v2',
      xid: 234911124,
      wid: 1,
      plotLayout: { type: LayoutType.GRID },
      plots: [
        {
          plotId: 'loss_kpi',
          title: 'Avg validation loss',
          type: PlotType.SUMMARY_BOX,
          data: { metrics: ['val/loss'] },
        },
      ],
    },
  ]);
}

describe('DashboardGridComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: DashboardGridHarness;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj('DashboardService', ['getChartData']);
    dashboardServiceSpy.getChartData.and.returnValue(
      of({
        plotsData: [],
      }),
    );

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: DashboardService, useValue: dashboardServiceSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(DashboardGridHarness);
  });

  it('should render 3 comparative experiment columns matching Figma', async () => {
    expect(await harness.getChartCount()).toBe(3);
  });
});
