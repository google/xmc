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
import * as echarts from 'echarts';

import { PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';

import { PlotCardComponent } from './plot_card';
import { PlotCardHarness } from './plot_card_harness';

@Component({
  standalone: true,
  imports: [PlotCardComponent],
  template: ` <xmc-plot-card [plotSpec]="spec()" [plotData]="data()" /> `,
})
class TestHostComponent {
  readonly spec = signal<PlotSpec>({
    plotId: 'loss_curve',
    title: 'Validation loss vs steps',
    type: PlotType.SCATTER,
    data: { metrics: ['loss/val'] },
  });

  readonly data = signal<PlotData | undefined>({
    plotId: 'loss_curve',
    type: 'SCATTER',
    scatterSeries: {
      metricName: 'loss/val',
      points: [
        { step: 0, avgValue: 1.0 },
        { step: 10, avgValue: 0.8 },
      ],
    },
  });
}

describe('PlotCardComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: PlotCardHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(PlotCardHarness);
  });

  it('should render ECharts canvas for SCATTER plot', async () => {
    expect(await harness.isSummaryBox()).toBeFalse();
    expect(await harness.getTitle()).toBe('Validation loss vs steps');
    expect(await harness.hasCanvas()).toBeTrue();
  });

  it('should render SummaryBoxComponent when PlotType is SUMMARY_BOX', async () => {
    host.spec.set({
      plotId: 'kpi_loss',
      title: 'Loss KPI',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['loss/val'] },
    });
    host.data.set({
      plotId: 'kpi_loss',
      type: 'SUMMARY_BOX',
      summaryData: {
        metricName: 'loss/val',
        latestValue: 0.65,
      },
    });
    fixture.detectChanges();

    expect(await harness.isSummaryBox()).toBeTrue();
    const summaryHarness = await harness.getSummaryBoxHarness();
    expect(summaryHarness).not.toBeNull();
    expect(await summaryHarness!.getValue()).toBe('0.65');
  });

  it('should reactively switch between SCATTER canvas and SUMMARY_BOX without errors', async () => {
    expect(await harness.isSummaryBox()).toBeFalse();
    expect(await harness.hasCanvas()).toBeTrue();

    host.spec.set({
      plotId: 'kpi_loss',
      title: 'Loss KPI',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['loss/val'] },
    });
    host.data.set({
      plotId: 'kpi_loss',
      type: 'SUMMARY_BOX',
      summaryData: {
        metricName: 'loss/val',
        latestValue: 0.88,
      },
    });
    fixture.detectChanges();
    expect(await harness.isSummaryBox()).toBeTrue();
    expect(await harness.hasCanvas()).toBeFalse();

    host.spec.set({
      plotId: 'loss_curve',
      title: 'Validation loss vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['loss/val'] },
    });
    host.data.set({
      plotId: 'loss_curve',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'loss/val',
        points: [{ step: 0, avgValue: 1.0 }],
      },
    });
    fixture.detectChanges();
    expect(await harness.isSummaryBox()).toBeFalse();
    expect(await harness.hasCanvas()).toBeTrue();
  });

  it('should initialize ECharts instance only once across multiple data updates', async () => {
    expect(await harness.isSummaryBox()).toBeFalse();
    expect(await harness.hasCanvas()).toBeTrue();

    const canvasEl = fixture.nativeElement.querySelector('.plot-canvas') as HTMLDivElement;
    expect(canvasEl).not.toBeNull();
    const initialInstance = echarts.getInstanceByDom(canvasEl);
    expect(initialInstance).toBeDefined();

    host.data.set({
      plotId: 'loss_curve',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'loss/val',
        points: [
          { step: 0, avgValue: 1.0 },
          { step: 10, avgValue: 0.8 },
          { step: 20, avgValue: 0.5 },
        ],
      },
    });
    fixture.detectChanges();

    host.data.set({
      plotId: 'loss_curve',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'loss/val',
        points: [
          { step: 0, avgValue: 1.0 },
          { step: 10, avgValue: 0.8 },
          { step: 20, avgValue: 0.5 },
          { step: 30, avgValue: 0.3 },
        ],
      },
    });
    fixture.detectChanges();

    const currentInstance = echarts.getInstanceByDom(canvasEl);
    expect(currentInstance).toBe(initialInstance);
  });
});
