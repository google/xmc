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
import {CommonModule, DatePipe} from '@angular/common';
import {ChangeDetectionStrategy, Component, computed, inject, input, signal} from '@angular/core';
import {toObservable} from '@angular/core/rxjs-interop';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {MatProgressBarModule} from '@angular/material/progress-bar';
import {Router, RouterLink} from '@angular/router';
import {QueueInfo, StructuredMessage} from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {combineLatest} from 'rxjs';
import {map} from 'rxjs/operators';

import {formatSource, StatusMessageFilterService} from '../../../service/status_message_filter_service';
import {parseWorkUnitName} from '../../resource_name_extractor';
import {ProcessedUrl} from '../../url/url_processor';

const STACK_TRACE_PREVIEW_LINES = 5;

/**
 * Component for displaying a status message, including aux information such as
 * a stack trace and scheduling information.
 */
@Component({
  selector: 'xmc-status-message',
  standalone: true,
  templateUrl: './status_message.ng.html',
  styleUrl: './status_message.css',
  imports: [
    CommonModule,
    DatePipe,
    MatButtonModule,
    MatIconModule,
    RouterLink,
    MatProgressBarModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusMessage {
  readonly message = input.required<StructuredMessage>();
  readonly ignoreFilters = input<boolean>(false);
  readonly showWorkUnitId = input<boolean>(false);
  private readonly filterService = inject(StatusMessageFilterService);
  private readonly router = inject(Router);
  protected readonly formatSource = formatSource;

  protected readonly stackTraceVisible = signal(false);

  toggleStackTrace() {
    this.stackTraceVisible.set(!this.stackTraceVisible());
  }

  protected getQueueProgress(queueInfo: QueueInfo): number {
    const p = queueInfo.positionInQueue;
    const t = queueInfo.totalWorkloadsInQueue;
    if (!p || !t || t <= 0) {
      return 0;
    }
    if (t === 1) {
      return 100;
    }
    return 100 * (t - p) / (t - 1);
  }

  protected readonly stackTraceLines = computed(
      () => this.message().auxiliaryInfo?.stackTrace?.split('\n') ?? []);
  protected readonly stackTracePreview = computed(
      () => this.stackTraceLines()
                .slice(0, STACK_TRACE_PREVIEW_LINES)
                .join('\n'));
  protected readonly stackTraceMore = computed(
      () => this.stackTraceLines().slice(STACK_TRACE_PREVIEW_LINES).join('\n'));
  protected readonly showMoreButton =
      computed(() => this.stackTraceLines().length > STACK_TRACE_PREVIEW_LINES);

  protected readonly lineClass =
      computed<'yellow-vertical-line'|'red-vertical-line'|'blue-vertical-line'|
               'grey-vertical-line'>(() => {
        const severity = this.message().severity;
        if (severity === 'WARNING') {
          return 'yellow-vertical-line';
        } else if (severity === 'ERROR' || severity === 'FATAL') {
          return 'red-vertical-line';
        } else if (severity === 'DEBUG') {
          return 'grey-vertical-line';
        } else {
          return 'blue-vertical-line';
        }
      });

  protected readonly urlsForWidget = computed<ProcessedUrl[]>(() => {
    const url = this.message().userFacingUrl;
    if (!url?.url) {
      return [];
    }
    return [
      {
        displayName: url.displayName || 'Open Link',
        icon: url.icon || 'link',
        processedUrl: url.url,
      },
    ];
  });

  protected readonly isVisible$ =
      combineLatest([
        toObservable(this.message),
        toObservable(this.ignoreFilters),
        this.filterService.severities$,
        this.filterService.sources$,
      ])
          .pipe(map(
              ([message, ignoreFilters, enabledSeverities, enabledSources]) => {
                return ignoreFilters ||
                    this.filterService.isMessageVisible(message);
              }));

  protected readonly workUnitId = computed(() => {
    if (!this.showWorkUnitId()) {
      return null;
    }
    const parent = this.message().parent;
    if (!parent) return null;
    const parsed = parseWorkUnitName(parent);
    return parsed ? parsed.wid : null;
  });

  protected shouldShowOriginLink(): boolean {
    const message = this.message();
    return !!message.parent && this.router.url !== `/${message.parent}`;
  }
}

/** Interface for object containing status messages. */
export interface StatusWithMessages {
  systemMessage?: StructuredMessage;
  userMessage?: StructuredMessage;
  schedulingInformation?: StructuredMessage;
}

/** Extracts status messages from a status object. */
export function getStatusMessages(status: StatusWithMessages|undefined):
    StructuredMessage[] {
  if (!status) return [];
  return [
    status.systemMessage,
    status.userMessage,
    status.schedulingInformation,
  ].filter((msg): msg is StructuredMessage => !!msg?.userFacingText);
}
