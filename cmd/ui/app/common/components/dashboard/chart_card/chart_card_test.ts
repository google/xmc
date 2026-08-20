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
import { of, Subject, throwError } from 'rxjs';

import {
  Chart,
  ChartData,
  LayoutType,
  PlotData,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { ChartCardComponent, getChartKey, hasWorkUnit, PlotDataPipe } from './chart_card';
import { ChartCardHarness } from './chart_card_harness';

@Component({
  standalone: true,
  imports: [ChartCardComponent],
  template: ` <xmc-chart-card [chart]="chart()" /> `,
})
class TestHostComponent {
  readonly chart = signal<Chart>({
    name: 'experiments/9001/workUnits/1/charts/mixed_overview',
    title: 'VCC Baseline',
    xid: 234911122,
    wid: 1,
    plotLayout: { type: LayoutType.GRID },
    plots: [
      {
        plotId: 'loss_kpi',
        title: 'Last validation loss',
        type: PlotType.SUMMARY_BOX,
        data: { metrics: ['train/loss'] },
      },
      {
        plotId: 'loss_curve',
        title: 'Validation loss vs steps',
        type: PlotType.SCATTER,
        data: { metrics: ['eval/loss'] },
      },
    ],
  });
}

describe('ChartCardComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: ChartCardHarness;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  const mockChartData: ChartData = {
    name: 'experiments/9001/workUnits/1/charts/mixed_overview',
    xid: '9001',
    wid: '1',
    plotsData: [
      {
        plotId: 'loss_kpi',
        type: 'SUMMARY_BOX',
        summaryData: {
          metricName: 'train/loss',
          latestValue: 0.65,
          meanValue: 0.73,
        },
      },
      {
        plotId: 'loss_curve',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'eval/loss',
          points: [
            { step: '0', avgValue: 1.85 },
            { step: '50', avgValue: 1.45 },
          ],
        },
      },
    ],
  };

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj('DashboardService', ['getChartData']);
    dashboardServiceSpy.getChartData.and.returnValue(of(mockChartData));

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: DashboardService, useValue: dashboardServiceSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(ChartCardHarness);
  });

  it('should render header with formatted experiment title and work unit link', async () => {
    expect(await harness.getHeaderTitle()).toBe('XID/234911122 - VCC Baseline - WorkUnitID 1');
    expect(await harness.getHeaderLinkHref()).toBe('/experiments/234911122/workUnits/1');
  });

  it('should fetch chart data on init', () => {
    expect(dashboardServiceSpy.getChartData).toHaveBeenCalledWith({
      name: 'experiments/9001/workUnits/1/charts/mixed_overview',
    });
  });

  it('should render child summary box and plot card', async () => {
    expect(await harness.getSummaryBoxCount()).toBe(1);
    expect(await harness.getPlotCardCount()).toBe(2);
  });

  it('should unsubscribe from previous request on consecutive fetchChartData calls', () => {
    const firstRequest = new Subject<ChartData>();
    const secondRequest = new Subject<ChartData>();
    dashboardServiceSpy.getChartData.and.returnValues(firstRequest, secondRequest);

    const chartCard = fixture.debugElement.children[0].componentInstance as ChartCardComponent;
    chartCard.fetchChartData();
    expect(firstRequest.observed).toBeTrue();

    chartCard.fetchChartData();
    expect(firstRequest.observed).toBeFalse();
    expect(secondRequest.observed).toBeTrue();
  });

  it('should unsubscribe on component destruction', () => {
    const subject = new Subject<ChartData>();
    dashboardServiceSpy.getChartData.and.returnValue(subject);

    const chartCard = fixture.debugElement.children[0].componentInstance as ChartCardComponent;
    chartCard.fetchChartData();
    expect(subject.observed).toBeTrue();

    fixture.destroy();
    expect(subject.observed).toBeFalse();
  });

  it('should set error message when getChartData fails', () => {
    dashboardServiceSpy.getChartData.and.returnValue(
      throwError(() => new Error('Failed to load chart metrics')),
    );

    const chartCard = fixture.debugElement.children[0].componentInstance as ChartCardComponent;
    chartCard.fetchChartData();

    const errorSignal = (chartCard as unknown as { error: () => string | null }).error;
    const isLoadingSignal = (chartCard as unknown as { isLoading: () => boolean }).isLoading;
    expect(errorSignal()).toBe('Failed to load chart metrics');
    expect(isLoadingSignal()).toBeFalse();
  });

  it('should compute getChartKey using name or composite xid_wid_title fallback', () => {
    expect(getChartKey({ name: 'experiments/1/workUnits/1/charts/c1', xid: 1 })).toBe(
      'experiments/1/workUnits/1/charts/c1',
    );
    expect(getChartKey({ xid: 123, wid: 2, title: 'eval' })).toBe('123_2_eval');
  });

  it('should look up plot data using cached plotDataMap via PlotDataPipe', () => {
    const pipe = new PlotDataPipe();
    const chartCard = fixture.debugElement.children[0].componentInstance as ChartCardComponent;
    const map = (
      chartCard as unknown as { plotDataMap: () => Map<string, PlotData> }
    ).plotDataMap();

    const lossKpi = pipe.transform('loss_kpi', map);
    expect(lossKpi?.plotId).toBe('loss_kpi');
    expect(pipe.transform('nonexistent', map)).toBeUndefined();
    expect(pipe.transform(undefined, map)).toBeUndefined();
  });

  it('should emit unique getChartKey when onRemove is called', () => {
    const chartCard = fixture.debugElement.children[0].componentInstance as ChartCardComponent;
    const emitSpy = spyOn(chartCard.remove, 'emit');
    chartCard.onRemove();
    expect(emitSpy).toHaveBeenCalledWith('experiments/9001/workUnits/1/charts/mixed_overview');
  });

  it('should correctly identify whether a chart has a work unit via hasWorkUnit', () => {
    expect(hasWorkUnit({ xid: 123, wid: 1, title: 'run 1' })).toBeTrue();
    expect(hasWorkUnit({ xid: 123, wid: '2', title: 'run 2' })).toBeTrue();
    expect(hasWorkUnit({ xid: 123, wid: 0, title: 'experiment level' })).toBeFalse();
    expect(hasWorkUnit({ xid: 123, wid: '0', title: 'experiment level' })).toBeFalse();
    expect(hasWorkUnit({ xid: 123, title: 'no wid' })).toBeFalse();
  });

  it('should omit WorkUnitID from title and link to experiment root when wid is 0 or unset', async () => {
    host.chart.set({
      name: 'experiments/9001/workUnits/0/charts/overview',
      title: 'Experiment Overview',
      xid: 234911122,
      wid: 0,
      plots: [],
    });
    fixture.detectChanges();

    expect(await harness.getHeaderTitle()).toBe('XID/234911122 - Experiment Overview');
    expect(await harness.getHeaderLinkHref()).toBe('/experiments/234911122');
  });
});
