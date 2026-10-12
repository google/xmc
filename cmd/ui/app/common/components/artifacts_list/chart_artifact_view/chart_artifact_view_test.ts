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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { of, throwError } from 'rxjs';

import {
  ChartArtifactPayload,
  ChartTypedArtifact,
  PayloadType,
} from 'app/common/artifacts/interfaces';
import { ChartData, LayoutType, PlotType } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';

import { ChartArtifactView, chartStatusName, isSummaryBoxPlot } from './chart_artifact_view';
import { ChartArtifactViewHarness } from './chart_artifact_view_harness';

const CHART_NAME = 'experiments/9001/workUnits/1/charts/b6f1c0a2';

function chartArtifact(payload: ChartArtifactPayload): ChartTypedArtifact {
  return {
    type: PayloadType.CHART,
    artifact: { name: 'experiments/9001/workUnits/1/artifacts/1' },
    payload,
  };
}

const PROVISIONED_PAYLOAD: ChartArtifactPayload = {
  title: 'Training progress',
  metricNames: ['chart_demo/loss', 'chart_demo/accuracy'],
  status: 'SUCCESS',
  chart: {
    name: CHART_NAME,
    title: 'Training progress',
    xid: 9001,
    wid: 1,
    plotLayout: { type: LayoutType.GRID, columns: 2 },
    plots: [
      {
        plotId: 'chart_demo_loss_latest',
        title: 'chart_demo/loss (latest)',
        type: PlotType.SUMMARY_BOX,
        data: { metrics: ['chart_demo/loss'] },
      },
      {
        plotId: 'chart_demo_loss_curve',
        title: 'chart_demo/loss vs steps',
        type: PlotType.SCATTER,
        data: { metrics: ['chart_demo/loss'] },
        mapping: { x: 'step' },
      },
    ],
  },
};

const MOCK_CHART_DATA: ChartData = {
  name: CHART_NAME,
  xid: '9001',
  wid: '1',
  plotsData: [
    {
      plotId: 'chart_demo_loss_latest',
      type: 'SUMMARY_BOX',
      summaryData: { metricName: 'chart_demo/loss', latestValue: 0.42, meanValue: 0.71 },
    },
    {
      plotId: 'chart_demo_loss_curve',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'chart_demo/loss',
        points: [
          { step: '0', avgValue: 1.8 },
          { step: '50', avgValue: 0.42 },
        ],
      },
    },
  ],
};

@Component({
  standalone: true,
  imports: [ChartArtifactView],
  template: ` <xmc-chart-artifact-view [artifact]="artifact()" /> `,
})
class TestHostComponent {
  readonly artifact = signal<ChartTypedArtifact>(chartArtifact(PROVISIONED_PAYLOAD));
}

describe('ChartArtifactView', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: ChartArtifactViewHarness;
  let dashboardServiceSpy: jasmine.SpyObj<DashboardService>;

  async function setUp(): Promise<void> {
    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    harness = await TestbedHarnessEnvironment.loader(fixture).getHarness(ChartArtifactViewHarness);
  }

  beforeEach(async () => {
    dashboardServiceSpy = jasmine.createSpyObj('DashboardService', ['getChartData']);
    dashboardServiceSpy.getChartData.and.returnValue(of(MOCK_CHART_DATA));

    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
      providers: [{ provide: DashboardService, useValue: dashboardServiceSpy }],
    }).compileComponents();
  });

  it('renders the artifact title and its metric names', async () => {
    await setUp();

    expect(await harness.getTitle()).toBe('Training progress');
    expect(await harness.getMetricNames()).toEqual(['chart_demo/loss', 'chart_demo/accuracy']);
  });

  it('fetches chart data for the chart recorded on the artifact', async () => {
    await setUp();

    expect(dashboardServiceSpy.getChartData).toHaveBeenCalledWith({ name: CHART_NAME });
  });

  it('draws the plot specs carried by the artifact', async () => {
    await setUp();

    expect(await harness.getNotice()).toBeNull();
    expect(await harness.getSummaryBoxCount()).toBe(1);
    expect(await harness.getPlotCardCount()).toBe(2);
  });

  it('explains that provisioning is unfinished instead of querying an unnamed chart', async () => {
    await setUp();
    host.artifact.set(
      chartArtifact({
        title: 'Training progress',
        metricNames: ['chart_demo/loss'],
        status: 'PROVISIONING',
      }),
    );
    dashboardServiceSpy.getChartData.calls.reset();
    fixture.detectChanges();

    expect(await harness.getNotice()).toContain('still being provisioned');
    expect(await harness.getPlotCardCount()).toBe(0);
    expect(dashboardServiceSpy.getChartData).not.toHaveBeenCalled();
  });

  it('surfaces the server error message when provisioning failed', async () => {
    await setUp();
    host.artifact.set(
      chartArtifact({
        title: 'Training progress',
        status: 'FAILED',
        errorMessage: 'dashboard service unavailable',
      }),
    );
    fixture.detectChanges();

    expect(await harness.getNotice()).toContain('dashboard service unavailable');
  });

  // A serializer that did not apply the proto JSON mapping sends ChartArtifact.Status as its
  // enum tag. Matching only the names let a numeric FAILED slip past the notice and render as
  // a healthy chart, swallowing the error message the payload was carrying.
  it('surfaces the server error message when the status arrived as an enum tag', async () => {
    await setUp();
    host.artifact.set(
      chartArtifact({
        title: 'Training progress',
        status: 3 as unknown as ChartArtifactPayload['status'],
        errorMessage: 'dashboard service unavailable',
      }),
    );
    fixture.detectChanges();

    expect(await harness.getNotice()).toContain('dashboard service unavailable');
  });

  it('says so when the provisioned chart declares no plots', async () => {
    await setUp();
    host.artifact.set(
      chartArtifact({
        title: 'Training progress',
        status: 'SUCCESS',
        chart: { name: CHART_NAME, xid: 9001, wid: 1, plots: [] },
      }),
    );
    fixture.detectChanges();

    expect(await harness.getNotice()).toContain('no plots');
  });

  // A different chart name from the one setUp fetched, so that leaving the plots out is the
  // only thing standing between this artifact and a request.
  it('does not query a chart that declares no plots', async () => {
    await setUp();
    host.artifact.set(
      chartArtifact({
        title: 'Training progress',
        status: 'SUCCESS',
        chart: { name: 'experiments/9001/workUnits/1/charts/empty', xid: 9001, wid: 1, plots: [] },
      }),
    );
    dashboardServiceSpy.getChartData.calls.reset();
    fixture.detectChanges();

    // Nothing would display the response, so the round trip -- and a data engine query per
    // plot behind it -- would be spent filling a map the template never reads.
    expect(dashboardServiceSpy.getChartData).not.toHaveBeenCalled();
  });

  it('falls back to a generic title when the artifact has none', async () => {
    await setUp();
    host.artifact.set(chartArtifact({ ...PROVISIONED_PAYLOAD, title: undefined }));
    fixture.detectChanges();

    expect(await harness.getTitle()).toBe('Chart artifact');
  });

  // The thrown message is deliberately not the component's fallback string: an assertion
  // against the fallback would pass whether or not the error is ever read. The banner is only
  // reached once loading has finished, so finding it also covers the loading flag clearing.
  it('reports a failed data fetch in an error banner', async () => {
    dashboardServiceSpy.getChartData.and.returnValue(
      throwError(() => new Error('chart data service is unavailable')),
    );
    await setUp();

    const banner = await harness.getErrorBanner();
    expect(banner).not.toBeNull();
    expect(await banner!.getMessage()).toBe('chart data service is unavailable');
  });

  it('falls back to a generic message when the failure carries none', async () => {
    dashboardServiceSpy.getChartData.and.returnValue(throwError(() => new Error()));
    await setUp();

    const banner = await harness.getErrorBanner();
    expect(await banner!.getMessage()).toBe('Failed to load chart metrics');
  });

  it('re-fetches and clears the banner when the retry is taken', async () => {
    dashboardServiceSpy.getChartData.and.returnValue(throwError(() => new Error('unavailable')));
    await setUp();
    const banner = await harness.getErrorBanner();

    dashboardServiceSpy.getChartData.and.returnValue(of(MOCK_CHART_DATA));
    await banner!.clickRetry();

    expect(dashboardServiceSpy.getChartData).toHaveBeenCalledTimes(2);
    expect(await harness.getErrorBanner()).toBeNull();
  });

  it('normalises a status given as an enum tag to its proto JSON name', () => {
    expect(chartStatusName('FAILED')).toBe('FAILED');
    expect(chartStatusName(0)).toBe('STATUS_UNSPECIFIED');
    expect(chartStatusName(1)).toBe('PROVISIONING');
    expect(chartStatusName(2)).toBe('SUCCESS');
    expect(chartStatusName(3)).toBe('FAILED');
    expect(chartStatusName(undefined)).toBeUndefined();
    // A tag this build has no name for is better left unmatched than mapped to a wrong status.
    expect(chartStatusName(99)).toBeUndefined();
  });

  it('recognises summary boxes by proto JSON name and by enum tag', () => {
    // The numeric tag is what a serializer that did not apply the proto JSON mapping emits.
    expect(isSummaryBoxPlot({ plotId: 'a', type: PlotType.SUMMARY_BOX })).toBeTrue();
    expect(isSummaryBoxPlot({ plotId: 'b', type: 1 })).toBeTrue();
    expect(isSummaryBoxPlot({ plotId: 'c', type: PlotType.SCATTER })).toBeFalse();
    expect(isSummaryBoxPlot({ plotId: 'd' })).toBeFalse();
  });
});
