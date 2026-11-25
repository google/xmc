// Copyright 2026 Google LLC
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
import { Component, signal } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { BehaviorSubject } from 'rxjs';

import { StructuredMessage } from 'app/protos/experiment_state_server';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';
import { bootstrap, cleanState, flush, getEl, hasEl, setupModule, trigger } from 'app/testing/test';

import { getStatusMessages, StatusMessage } from './status_message';

const CONTAINER_SELECTOR = 'div[class$="-message-container"]';
const TEXT_SELECTOR = '.text';
const QUEUE_INFO_SELECTOR = '.queue-info';
const STACK_TRACE_BTN_SELECTOR = '.stack-trace-button';
const STACK_TRACE_MORE_SELECTOR = '.stack-trace-more-pre';
const METADATA_SELECTOR = '.metadata';

class StubStatusMessageFilterService implements Pick<
  StatusMessageFilterService,
  'severities$' | 'sources$' | 'isMessageVisible'
> {
  readonly severitiesSubject = new BehaviorSubject<Set<StructuredMessage.Severity>>(
    new Set(['WARNING', 'ERROR', 'FATAL']),
  );
  readonly sourcesSubject = new BehaviorSubject<Set<StructuredMessage.Source>>(
    new Set(['APPLICATION', 'XMANAGER_BACKEND']),
  );
  readonly severities$ = this.severitiesSubject.asObservable();
  readonly sources$ = this.sourcesSubject.asObservable();

  isMessageVisible(message: StructuredMessage): boolean {
    const severity = message.severity ?? 'SEVERITY_UNSPECIFIED';
    const source = message.source ?? 'SOURCE_UNSPECIFIED';
    return (
      this.severitiesSubject.getValue().has(severity) && this.sourcesSubject.getValue().has(source)
    );
  }
}

@Component({
  standalone: true,
  imports: [StatusMessage],
  template: `
    <xmc-status-message
      [message]="message()"
      [ignoreFilters]="ignoreFilters()"
      [showWorkUnitId]="showWorkUnitId()"
    />
  `,
})
class TestHostComponent {
  readonly message = signal<StructuredMessage>({
    userFacingText: 'Default message',
    severity: 'INFO',
    source: 'APPLICATION',
  });
  readonly ignoreFilters = signal(true);
  readonly showWorkUnitId = signal(false);
}

describe('StatusMessage', () => {
  const state = cleanState(() => {
    const filterService = new StubStatusMessageFilterService();
    setupModule({
      imports: [StatusMessage, TestHostComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        { provide: StatusMessageFilterService, useValue: filterService },
      ],
    });
    return { filterService };
  }, beforeEach);

  const severityCases: ReadonlyArray<{
    label: string;
    severity: StructuredMessage.Severity | undefined;
    expectedPrefix: string;
    expectedLineClass: string;
  }> = [
    {
      label: 'DEBUG',
      severity: 'DEBUG',
      expectedPrefix: 'debug',
      expectedLineClass: 'grey-vertical-line',
    },
    {
      label: 'INFO',
      severity: 'INFO',
      expectedPrefix: 'info',
      expectedLineClass: 'blue-vertical-line',
    },
    {
      label: 'WARNING',
      severity: 'WARNING',
      expectedPrefix: 'warning',
      expectedLineClass: 'yellow-vertical-line',
    },
    {
      label: 'ERROR',
      severity: 'ERROR',
      expectedPrefix: 'error',
      expectedLineClass: 'red-vertical-line',
    },
    {
      label: 'FATAL',
      severity: 'FATAL',
      expectedPrefix: 'fatal',
      expectedLineClass: 'red-vertical-line',
    },
    {
      label: 'SEVERITY_UNSPECIFIED',
      severity: 'SEVERITY_UNSPECIFIED',
      expectedPrefix: 'info',
      expectedLineClass: 'blue-vertical-line',
    },
    {
      label: 'undefined',
      severity: undefined,
      expectedPrefix: 'info',
      expectedLineClass: 'blue-vertical-line',
    },
    {
      label: 'unknown severity',
      severity: 'CUSTOM_SEVERITY',
      expectedPrefix: 'info',
      expectedLineClass: 'blue-vertical-line',
    },
  ];

  for (const tc of severityCases) {
    it(`maps ${tc.label} to ${tc.expectedPrefix} container, text, and line classes`, () => {
      bootstrap(StatusMessage, {
        message: {
          userFacingText: `Payload for ${tc.label}`,
          severity: tc.severity,
          source: 'APPLICATION',
        },
        ignoreFilters: true,
      });

      const containerEl = getEl(CONTAINER_SELECTOR);
      expect(containerEl).not.toBeNull();
      expect(containerEl.classList.contains(`${tc.expectedPrefix}-message-container`)).toBeTrue();
      expect(containerEl.classList.contains('severity_unspecified-message-container')).toBeFalse();

      const lineEl = containerEl.firstElementChild as HTMLElement;
      const bodyEl = containerEl.lastElementChild as HTMLElement;
      expect(lineEl.classList.contains(tc.expectedLineClass)).toBeTrue();
      expect(bodyEl.classList.contains(`${tc.expectedPrefix}-message`)).toBeTrue();
      expect(getEl(TEXT_SELECTOR).textContent?.trim()).toBe(`Payload for ${tc.label}`);
    });
  }

  it('updates severity styling dynamically when [message] input changes', async () => {
    const hostComp = bootstrap(TestHostComponent);

    let containerEl = getEl(CONTAINER_SELECTOR);
    expect(containerEl.classList.contains('info-message-container')).toBeTrue();
    expect(
      (containerEl.firstElementChild as HTMLElement).classList.contains('blue-vertical-line'),
    ).toBeTrue();

    hostComp.message.set({
      userFacingText: 'Escalated error',
      severity: 'ERROR',
      source: 'APPLICATION',
    });
    await flush();

    containerEl = getEl(CONTAINER_SELECTOR);
    expect(containerEl.classList.contains('info-message-container')).toBeFalse();
    expect(containerEl.classList.contains('error-message-container')).toBeTrue();
    expect(
      (containerEl.firstElementChild as HTMLElement).classList.contains('red-vertical-line'),
    ).toBeTrue();
    expect(getEl(TEXT_SELECTOR).textContent?.trim()).toBe('Escalated error');

    hostComp.message.set({
      userFacingText: 'Back to unspecified',
      severity: 'SEVERITY_UNSPECIFIED',
      source: 'APPLICATION',
    });
    await flush();

    containerEl = getEl(CONTAINER_SELECTOR);
    expect(containerEl.classList.contains('error-message-container')).toBeFalse();
    expect(containerEl.classList.contains('info-message-container')).toBeTrue();
    expect(
      (containerEl.firstElementChild as HTMLElement).classList.contains('blue-vertical-line'),
    ).toBeTrue();
  });

  it('respects filterService when ignoreFilters is false and re-evaluates on filter changes', async () => {
    const hostComp = bootstrap(TestHostComponent);
    hostComp.ignoreFilters.set(false);
    await flush();

    expect(hasEl(CONTAINER_SELECTOR)).toBeFalse();

    state.filterService.severitiesSubject.next(new Set(['INFO', 'WARNING', 'ERROR', 'FATAL']));
    await flush();

    expect(hasEl(CONTAINER_SELECTOR)).toBeTrue();
  });

  it('toggles long stack traces and renders queue info and work unit metadata', async () => {
    bootstrap(StatusMessage, {
      message: {
        userFacingText: 'Queued with trace',
        severity: 'WARNING',
        source: 'XMANAGER_BACKEND',
        parent: 'experiments/1/workUnits/7',
        auxiliaryInfo: {
          queueInfo: { positionInQueue: 2, totalWorkloadsInQueue: 5 },
          stackTrace: 'line1\nline2\nline3\nline4\nline5\nline6',
        },
      },
      ignoreFilters: true,
      showWorkUnitId: true,
    });

    expect(getEl(QUEUE_INFO_SELECTOR).textContent).toContain('Position in queue: 2 of 5');
    expect(getEl(METADATA_SELECTOR).textContent).toContain('Work Unit: 7');
    expect(hasEl(STACK_TRACE_MORE_SELECTOR)).toBeFalse();

    await trigger(getEl(STACK_TRACE_BTN_SELECTOR), 'click');
    expect(hasEl(STACK_TRACE_MORE_SELECTOR)).toBeTrue();
    expect(getEl(STACK_TRACE_MORE_SELECTOR).textContent).toContain('line6');
  });
});

describe('getStatusMessages', () => {
  it('filters undefined or empty status message fields', () => {
    expect(getStatusMessages(undefined)).toEqual([]);
    const result = getStatusMessages({
      systemMessage: { userFacingText: 'System', severity: 'ERROR' },
      userMessage: { userFacingText: '' },
      schedulingInformation: { userFacingText: 'Queued', severity: 'INFO' },
    });
    expect(result.length).toBe(2);
    expect(result.map((msg) => msg.userFacingText)).toEqual(['System', 'Queued']);
  });
});
