// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
import { ChangeDetectionStrategy, Component, computed, input, output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatNativeDateModule } from '@angular/material/core';
import { MatDatepickerModule } from '@angular/material/datepicker';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';

import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {
  ALL_SEVERITIES,
  ALL_SOURCES,
  formatSource,
} from 'xmanager_cloud/ui/app/service/status_message_filter_service';

/**
 * Available presets for time filter.
 */
export enum TimePreset {
  ALL = 'ALL',
  LAST_1_HOUR = '1H',
  LAST_24_HOURS = '24H',
  LAST_7_DAYS = '7D',
  CUSTOM = 'CUSTOM',
}

/**
 * Represents the time filter interval preset and optional custom range bounds.
 */
export interface TimeFilterValue {
  preset: TimePreset;
  start?: string;
  end?: string;
}

/**
 * Component for filter and sort options on the status messages history list.
 */
@Component({
  selector: 'xmc-status-messages-list-filter',
  standalone: true,
  templateUrl: './status_messages_list_filter.ng.html',
  styleUrl: './status_messages_list_filter.css',
  imports: [
    FormsModule,
    MatButtonModule,
    MatDatepickerModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatNativeDateModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusMessagesListFilter {
  readonly severityFilter = input<readonly string[]>([]);
  readonly sourceFilter = input<readonly string[]>([]);
  readonly sortOrder = input<string>('DESC');
  readonly timeFilter = input<TimeFilterValue>({ preset: TimePreset.ALL });

  readonly severityFilterChange = output<readonly string[]>();
  readonly sourceFilterChange = output<readonly string[]>();
  readonly sortOrderChange = output<string>();
  readonly timeFilterChange = output<TimeFilterValue>();
  readonly refresh = output<void>();

  protected readonly severityOptions = ALL_SEVERITIES.map((sev) => ({
    value: sev,
    label: this.formatSeverity(sev),
  }));
  protected readonly sourceOptions = ALL_SOURCES.map((src) => ({
    value: src,
    label: formatSource(src),
  }));
  protected readonly timeOptions = [
    { value: TimePreset.ALL, label: 'All time' },
    { value: TimePreset.LAST_1_HOUR, label: 'Last 1 hour' },
    { value: TimePreset.LAST_24_HOURS, label: 'Last 24 hours' },
    { value: TimePreset.LAST_7_DAYS, label: 'Last 7 days' },
    { value: TimePreset.CUSTOM, label: 'Custom range...' },
  ];

  protected readonly selectedSeveritiesLabel = computed(() =>
    this.getSelectionLabel(this.severityFilter(), this.severityOptions.length, (s) =>
      this.formatSeverity(s),
    ),
  );

  protected readonly selectedSourcesLabel = computed(() =>
    this.getSelectionLabel(this.sourceFilter(), this.sourceOptions.length, (s) =>
      formatSource(s as StructuredMessage.Source),
    ),
  );

  protected readonly customTimeRangeSelected = computed(
    () => this.timeFilter().preset === TimePreset.CUSTOM,
  );

  protected readonly customStartDate = computed(() => {
    const start = this.timeFilter().start;
    return start ? new Date(start) : null;
  });

  protected readonly customStartTime = computed(() =>
    this.formatTimeString(this.timeFilter().start),
  );

  protected readonly customEndDate = computed(() => {
    const end = this.timeFilter().end;
    return end ? new Date(end) : null;
  });

  protected readonly customEndTime = computed(() => this.formatTimeString(this.timeFilter().end));

  protected readonly hasInvalidCustomRange = computed(() => {
    const filter = this.timeFilter();
    if (filter.preset !== TimePreset.CUSTOM || !filter.start || !filter.end) {
      return false;
    }
    return new Date(filter.start) >= new Date(filter.end);
  });

  protected onPresetChange(preset: TimePreset): void {
    const current = this.timeFilter();
    this.timeFilterChange.emit({
      preset,
      start: current.start,
      end: current.end,
    });
  }

  protected onCustomStartChange(date?: Date): void {
    const current = this.timeFilter();
    if (!date || isNaN(date.getTime())) {
      this.timeFilterChange.emit({
        preset: current.preset,
        start: undefined,
        end: current.end,
      });
      return;
    }
    if (current.start) {
      const prevDate = new Date(current.start);
      if (!isNaN(prevDate.getTime())) {
        date.setHours(prevDate.getHours());
        date.setMinutes(prevDate.getMinutes());
        date.setSeconds(0);
        date.setMilliseconds(0);
      }
    }
    this.timeFilterChange.emit({
      preset: current.preset,
      start: date.toISOString(),
      end: current.end,
    });
  }

  protected onCustomStartTimeChange(timeString: string): void {
    const current = this.timeFilter();
    const baseDate = current.start ? new Date(current.start) : new Date();
    if (isNaN(baseDate.getTime())) return;

    if (!timeString) {
      baseDate.setHours(0);
      baseDate.setMinutes(0);
      baseDate.setSeconds(0);
      baseDate.setMilliseconds(0);
    } else {
      const [hours, minutes] = timeString.split(':').map(Number);
      if (hours !== undefined && minutes !== undefined) {
        baseDate.setHours(hours);
        baseDate.setMinutes(minutes);
        baseDate.setSeconds(0);
        baseDate.setMilliseconds(0);
      }
    }
    this.timeFilterChange.emit({
      preset: current.preset,
      start: baseDate.toISOString(),
      end: current.end,
    });
  }

  protected onCustomEndChange(date?: Date): void {
    const current = this.timeFilter();
    if (!date || isNaN(date.getTime())) {
      this.timeFilterChange.emit({
        preset: current.preset,
        start: current.start,
        end: undefined,
      });
      return;
    }
    if (current.end) {
      const prevDate = new Date(current.end);
      if (!isNaN(prevDate.getTime())) {
        date.setHours(prevDate.getHours());
        date.setMinutes(prevDate.getMinutes());
        date.setSeconds(0);
        date.setMilliseconds(0);
      }
    }
    this.timeFilterChange.emit({
      preset: current.preset,
      start: current.start,
      end: date.toISOString(),
    });
  }

  protected onCustomEndTimeChange(timeString: string): void {
    const current = this.timeFilter();
    const baseDate = current.end ? new Date(current.end) : new Date();
    if (isNaN(baseDate.getTime())) return;

    if (!timeString) {
      baseDate.setHours(0);
      baseDate.setMinutes(0);
      baseDate.setSeconds(0);
      baseDate.setMilliseconds(0);
    } else {
      const [hours, minutes] = timeString.split(':').map(Number);
      if (hours !== undefined && minutes !== undefined) {
        baseDate.setHours(hours);
        baseDate.setMinutes(minutes);
        baseDate.setSeconds(0);
        baseDate.setMilliseconds(0);
      }
    }
    this.timeFilterChange.emit({
      preset: current.preset,
      start: current.start,
      end: baseDate.toISOString(),
    });
  }

  private formatSeverity(sev: string): string {
    return sev.charAt(0).toUpperCase() + sev.slice(1).toLowerCase();
  }

  private getSelectionLabel(
    selected: readonly string[],
    allCount: number,
    formatFn: (s: string) => string,
  ): string {
    if (selected.length === allCount) return 'All';
    return selected.map(formatFn).join(', ');
  }

  private static readonly timeFormatter = new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });

  private formatTimeString(dateStr?: string): string {
    if (!dateStr) return '';
    const date = new Date(dateStr);
    if (isNaN(date.getTime())) return '';
    return StatusMessagesListFilter.timeFormatter.format(date);
  }
}
