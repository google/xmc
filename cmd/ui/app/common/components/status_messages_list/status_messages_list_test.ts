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
import { setupModule, bootstrap, get, flush } from 'xmanager_cloud/ui/app/testing/test';
import 'jasmine';

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ListStatusMessagesResponse } from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { StatusMessage as StatusMessageProto } from 'xmanager_cloud/experiment_state_server/proto/status_message.jsonpb_decls';
import { MOCK_HISTORICAL_STATUS_MESSAGES } from 'xmanager_cloud/ui/app/common/mock_data';
import {
  ALL_SEVERITIES,
  StatusMessageFilterService,
} from 'xmanager_cloud/ui/app/service/status_message_filter_service';
import { StatusMessageService } from 'xmanager_cloud/ui/app/service/status_message_service';
import { of, throwError } from 'rxjs';

import { StatusMessagesList } from './status_messages_list';
import { StatusMessagesListHarness } from './testing/status_messages_list_harness';

describe('StatusMessagesList', () => {
  let mockStatusMessageService: jasmine.SpyObj<StatusMessageService>;

  beforeEach(() => {
    mockStatusMessageService = jasmine.createSpyObj('StatusMessageService', ['listStatusMessages']);
    mockStatusMessageService.listStatusMessages.and.callFake((req) => {
      let msgs = MOCK_HISTORICAL_STATUS_MESSAGES;
      if (req.filter) {
        if (req.filter.includes('severity = "ERROR"')) {
          msgs = msgs.filter((m) => m.content?.severity === 'ERROR');
        }
        if (req.filter.includes('create_time >=')) {
          msgs = msgs.slice(0, 1);
        }
      }
      return of({
        statusMessages: msgs,
      });
    });

    setupModule({
      imports: [StatusMessagesList],
      providers: [
        provideNoopAnimations(),
        { provide: StatusMessageService, useValue: mockStatusMessageService },
      ],
    });

    const filterService = get(StatusMessageFilterService);
    filterService.updateSeverities(new Set(ALL_SEVERITIES));
  });

  it('should create', () => {
    const component = bootstrap(StatusMessagesList);
    expect(component).toBeTruthy();
  });

  it('should display status messages when loaded successfully', async () => {
    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();

    const harness = await getHarness(StatusMessagesListHarness);
    expect(await harness.getMessagesCount()).toBe(2);
    expect(await harness.getMessageText(0)).toContain('Application is being initialized.');
  });

  it('should filter messages by severity', async () => {
    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();
    const harness = await getHarness(StatusMessagesListHarness);

    await (await harness.getFilter()).selectOnlySeverity('Error');
    await flush();

    expect(await harness.getMessagesCount()).toBe(1);
    expect(await harness.getMessageText(0)).toContain('Critical failure: connection lost.');
  });

  it('should load more messages when loadMore is called', async () => {
    mockStatusMessageService.listStatusMessages.and.returnValues(
      of({
        statusMessages: MOCK_HISTORICAL_STATUS_MESSAGES,
        nextPageToken: 'token-1',
      }),
      of({
        statusMessages: MOCK_HISTORICAL_STATUS_MESSAGES,
        nextPageToken: '',
      }),
    );
    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();

    const harness = await getHarness(StatusMessagesListHarness);
    const initialMessagesCount = await harness.getMessagesCount();
    await harness.clickLoadMore();
    await flush();

    expect(initialMessagesCount).toBe(2);
    expect(await harness.getMessagesCount()).toBe(4);
    expect(await harness.hasLoadMoreButton()).toBeFalse();
  });

  it('should display error message when loading fails', async () => {
    mockStatusMessageService.listStatusMessages.and.returnValue(
      throwError(() => ({ message: 'Network error' })),
    );

    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();

    const harness = await getHarness(StatusMessagesListHarness);
    expect(await harness.hasError()).toBeTrue();
    expect(await harness.getErrorText()).toBe('Network error');
  });

  it('should filter messages by source', async () => {
    const testMessages = [
      {
        content: {
          userFacingText: 'App message',
          severity: 'INFO',
          source: 'APPLICATION',
        },
      },
      {
        content: {
          userFacingText: 'K8s message',
          severity: 'ERROR',
          source: 'KUBERNETES',
        },
      },
    ] as StatusMessageProto[];
    mockStatusMessageService.listStatusMessages.and.callFake((req) => {
      let msgs = testMessages;
      if (req.filter && req.filter.includes('source = "KUBERNETES"')) {
        msgs = msgs.filter((m) => m.content?.source === 'KUBERNETES');
      }
      return of({ statusMessages: msgs } as ListStatusMessagesResponse);
    });
    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();
    const harness = await getHarness(StatusMessagesListHarness);

    await (await harness.getFilter()).selectOnlySource('Kubernetes');
    await flush();
    expect(await harness.getMessagesCount()).toBe(1);
    expect(await harness.getMessageText(0)).toContain('K8s message');
  });

  describe('sorting', () => {
    const testMessages = [
      {
        content: {
          userFacingText: 'Msg 1',
          createTime: '2025-10-23T17:40:00Z',
          severity: 'INFO',
          source: 'APPLICATION',
        },
      },
      {
        content: {
          userFacingText: 'Msg 2',
          createTime: '2025-10-23T17:41:00Z',
          severity: 'INFO',
          source: 'APPLICATION',
        },
      },
    ] as StatusMessageProto[];

    beforeEach(() => {
      mockStatusMessageService.listStatusMessages.and.callFake((req) => {
        const msgs = [...testMessages];
        if (req.orderBy && req.orderBy.includes('ASC')) {
          msgs.sort((a, b) =>
            (a.content?.createTime || '').localeCompare(b.content?.createTime || ''),
          );
        } else if (req.orderBy && req.orderBy.includes('DESC')) {
          msgs.sort((a, b) =>
            (b.content?.createTime || '').localeCompare(a.content?.createTime || ''),
          );
        }

        return of({ statusMessages: msgs } as ListStatusMessagesResponse);
      });
    });

    it('should sort messages in descending order by default', async () => {
      bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
      await flush();

      const harness = await getHarness(StatusMessagesListHarness);

      // Default is DESC (Newest first)
      expect(await harness.getMessageText(0)).toContain('Msg 2');
      expect(await harness.getMessageText(1)).toContain('Msg 1');
    });

    it('should sort messages in ascending order', async () => {
      bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
      await flush();
      const harness = await getHarness(StatusMessagesListHarness);

      await (await harness.getFilter()).selectSortOrder('Oldest First');
      await flush();

      // Now ASC (Oldest first)
      expect(await harness.getMessageText(0)).toContain('Msg 1');
      expect(await harness.getMessageText(1)).toContain('Msg 2');
    });
  });

  it('should refresh messages and reload on clicking refresh', async () => {
    bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
    await flush();
    const harness = await getHarness(StatusMessagesListHarness);
    const initialMessagesCount = await harness.getMessagesCount();

    mockStatusMessageService.listStatusMessages.and.returnValue(
      of({
        statusMessages: [
          ...MOCK_HISTORICAL_STATUS_MESSAGES,
          {
            content: {
              userFacingText: 'Msg 3',
              createTime: '2025-10-23T17:42:00Z',
              severity: 'INFO',
              source: 'APPLICATION',
            },
          },
        ],
      } as ListStatusMessagesResponse),
    );
    const filterHarness = await harness.getFilter();
    await filterHarness.clickRefresh();
    await flush();

    expect(initialMessagesCount).toBe(2);
    expect(await harness.getMessagesCount()).toBe(3);
  });

  describe('time filters', () => {
    beforeEach(() => {
      jasmine.clock().install();
      jasmine.clock().mockDate(new Date('2025-10-23T18:00:00.000Z'));
    });

    afterEach(() => {
      jasmine.clock().uninstall();
    });

    const timePresetCases = [
      {
        presetName: 'All time',
        expectedFilter: undefined,
      },
      {
        presetName: 'Last 1 hour',
        expectedFilter: 'create_time >= "2025-10-23T17:00:00.000Z"',
      },
      {
        presetName: 'Last 24 hours',
        expectedFilter: 'create_time >= "2025-10-22T18:00:00.000Z"',
      },
      {
        presetName: 'Last 7 days',
        expectedFilter: 'create_time >= "2025-10-16T18:00:00.000Z"',
      },
    ];

    for (const tc of timePresetCases) {
      it(`should send correct filter to backend for time preset selection: ${tc.presetName}`, async () => {
        bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
        await flush();
        const harness = await getHarness(StatusMessagesListHarness);

        await (await harness.getFilter()).selectTimePreset(tc.presetName);
        await flush();

        const lastCall = mockStatusMessageService.listStatusMessages.calls.mostRecent();
        if (tc.expectedFilter) {
          expect(lastCall.args[0].filter).toContain(tc.expectedFilter);
        } else {
          expect(lastCall.args[0].filter).toBeUndefined();
        }
      });
    }

    it('should send correct filter payload for custom time range', async () => {
      bootstrap(StatusMessagesList, { parentName: 'experiments/1' });
      await flush();
      const harness = await getHarness(StatusMessagesListHarness);

      const filterHarness = await harness.getFilter();
      await filterHarness.selectTimePreset('Custom range...');
      await flush();
      await filterHarness.setCustomStart('10/01/2025');
      await filterHarness.setCustomEnd('10/02/2025');
      await filterHarness.clickRefresh();
      await flush();

      const lastCall = mockStatusMessageService.listStatusMessages.calls.mostRecent();
      const startIso = new Date('2025-10-01T00:00:00').toISOString();
      const endIso = new Date('2025-10-02T00:00:00').toISOString();
      expect(lastCall.args[0].filter).toContain(`create_time >= "${startIso}"`);
      expect(lastCall.args[0].filter).toContain(`create_time <= "${endIso}"`);
    });
  });
});
