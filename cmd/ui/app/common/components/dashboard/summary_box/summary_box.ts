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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';

import { MetricPolarity, SummaryBoxValue } from 'xmanager_cloud/ui/app/service/dashboard_models';

function formatScalarValue(val: number | undefined | null, metricName: string): string {
  if (val === undefined || val === null || isNaN(val)) return '—';

  const isPct = /acc|pct|percent/i.test(metricName);
  if (isPct) {
    const scaled = val <= 1.0 ? val * 100 : val;
    return `${scaled.toFixed(1)}%`;
  }
  if (Math.abs(val) < 0.001 && val !== 0) {
    return val.toExponential(3);
  }
  return val.toLocaleString(undefined, { maximumFractionDigits: 4 });
}

function resolvePolarity(polarity: MetricPolarity | undefined, metricName: string): MetricPolarity {
  if (polarity) return polarity;
  return /loss|err|cost/i.test(metricName)
    ? MetricPolarity.LOWER_IS_BETTER
    : MetricPolarity.HIGHER_IS_BETTER;
}

function getDeltaDifference(s: SummaryBoxValue): number | null {
  if (s.deltaVsBaseline !== undefined && !isNaN(s.deltaVsBaseline)) {
    return s.deltaVsBaseline;
  }
  if (s.baselineValue !== undefined && s.latestValue !== undefined) {
    return s.latestValue - s.baselineValue;
  }
  return null;
}

function getDeltaSentiment(isImproved: boolean, isWorse: boolean): string {
  if (isImproved) return 'better than baseline';
  if (isWorse) return 'worse than baseline';
  return 'equal to baseline';
}

function computeDeltaInfo(s: SummaryBoxValue) {
  const delta = getDeltaDifference(s);
  if (delta === null || isNaN(delta)) return null;

  const metricName = s.metricName ?? '';
  const isLowerBetter = resolvePolarity(s.polarity, metricName) === MetricPolarity.LOWER_IS_BETTER;
  const isImproved = isLowerBetter ? delta < 0 : delta > 0;
  const isWorse = isLowerBetter ? delta > 0 : delta < 0;

  const absDelta = Math.abs(delta);
  const isPct = /acc|pct/i.test(metricName);
  const formattedDelta = isPct ? `${(absDelta * 100).toFixed(2)}%` : absDelta.toFixed(2);
  const sentiment = getDeltaSentiment(isImproved, isWorse);

  return {
    deltaText: `${formattedDelta} ${sentiment}`,
    arrowIcon: delta < 0 ? 'arrow_downward' : 'arrow_upward',
    isImproved,
    isWorse,
  };
}

function computeDisplayTitle(s: SummaryBoxValue, customTitle?: string): string {
  if (customTitle) return customTitle;
  const metric = s.metricName ?? 'Metric';
  if (metric.includes('val') && metric.includes('loss')) return 'Last validation loss';
  if (metric.includes('train') && metric.includes('acc')) return 'Last training accuracy';
  if (metric.includes('loss')) return 'Last loss';
  if (metric.includes('acc')) return 'Last accuracy';
  return metric;
}

/**
 * Summary box KPI card rendering scalar metric values with delta against baseline.
 */
@Component({
  selector: 'xmc-summary-box',
  standalone: true,
  templateUrl: './summary_box.ng.html',
  styleUrl: './summary_box.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, MatCardModule, MatIconModule],
})
export class SummaryBoxComponent {
  readonly summary = input.required<SummaryBoxValue>();
  readonly title = input<string>();

  /**
   * Title displayed on top of the card.
   */
  protected readonly displayTitle = computed(() =>
    computeDisplayTitle(this.summary(), this.title()),
  );

  /**
   * Formatted scalar value.
   */
  protected readonly formattedValue = computed(() => {
    const s = this.summary();
    const val = s.latestValue ?? s.meanValue;
    return formatScalarValue(val, s.metricName ?? '');
  });

  /**
   * Delta analysis vs baseline.
   */
  protected readonly deltaInfo = computed(() => computeDeltaInfo(this.summary()));
}
