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

import { MetricPolarity, SummaryBoxValue } from 'xmanager_cloud/ui/app/service/dashboard_models';

import { SummaryBoxComponent } from './summary_box';
import { SummaryBoxHarness } from './summary_box_harness';

@Component({
  standalone: true,
  imports: [SummaryBoxComponent],
  template: ` <xmc-summary-box [summary]="summary()" [title]="title()" /> `,
})
class TestHostComponent {
  readonly summary = signal<SummaryBoxValue>({
    metricName: 'train/loss',
    latestValue: 0.1631,
    meanValue: 0.7398,
    minValue: 0.1631,
    maxValue: 2.0,
    latestStep: '500',
    latestTimestamp: '2026-08-11T11:23:20Z',
  });
  readonly title = signal<string | undefined>(undefined);
}

describe('SummaryBoxComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;
  let harness: SummaryBoxHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(SummaryBoxHarness);
  });

  it('should render scalar value from golden summary_box_single', async () => {
    expect(await harness.getTitle()).toBe('Last loss');
    expect(await harness.getValue()).toBe('0.1631');
    expect(await harness.hasDelta()).toBeFalse();
  });

  it('should render explicit title override', async () => {
    host.title.set('Custom KPI Title');
    fixture.detectChanges();
    expect(await harness.getTitle()).toBe('Custom KPI Title');
  });

  it('should render percentage values correctly', async () => {
    host.summary.set({
      metricName: 'eval/accuracy',
      latestValue: 0.924,
    });
    fixture.detectChanges();
    expect(await harness.getTitle()).toBe('Last accuracy');
    expect(await harness.getValue()).toBe('92.4%');
  });

  it('should render delta badge with improved sentiment for lower loss', async () => {
    host.summary.set({
      metricName: 'val/loss',
      latestValue: 0.65,
      baselineValue: 0.72,
      polarity: MetricPolarity.LOWER_IS_BETTER,
    });
    fixture.detectChanges();

    expect(await harness.hasDelta()).toBeTrue();
    const deltaText = await harness.getDeltaText();
    expect(deltaText).toContain('0.07 better than baseline');
    expect(await harness.isDeltaImproved()).toBeTrue();
    expect(await harness.isDeltaWorse()).toBeFalse();
  });

  it('should render delta badge with worse sentiment for higher loss', async () => {
    host.summary.set({
      metricName: 'val/loss',
      latestValue: 0.8,
      baselineValue: 0.73,
      polarity: MetricPolarity.LOWER_IS_BETTER,
    });
    fixture.detectChanges();

    expect(await harness.hasDelta()).toBeTrue();
    const deltaText = await harness.getDeltaText();
    expect(deltaText).toContain('0.07 worse than baseline');
    expect(await harness.isDeltaWorse()).toBeTrue();
    expect(await harness.isDeltaImproved()).toBeFalse();
  });

  it('should render delta badge with improved sentiment for higher accuracy', async () => {
    host.summary.set({
      metricName: 'train/accuracy',
      latestValue: 0.924,
      baselineValue: 0.9235,
      polarity: MetricPolarity.HIGHER_IS_BETTER,
    });
    fixture.detectChanges();

    expect(await harness.hasDelta()).toBeTrue();
    const deltaText = await harness.getDeltaText();
    expect(deltaText).toContain('better than baseline');
    expect(await harness.isDeltaImproved()).toBeTrue();
  });
});
