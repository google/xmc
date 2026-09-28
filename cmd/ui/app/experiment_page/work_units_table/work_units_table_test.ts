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
import 'jasmine';

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatTooltipHarness } from '@angular/material/tooltip/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { BehaviorSubject, NEVER, of } from 'rxjs';

import { ListWorkUnitsResponse } from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';
import { StatusMessageFilterService } from 'xmanager_cloud/ui/app/service/status_message_filter_service';
import { WorkUnitsService } from 'xmanager_cloud/ui/app/service/work_units_service';
import { bootstrap, cleanState, getEls, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { WorkUnitsTableHarness } from './testing/work_units_table_harness';
import { WorkUnitsTable } from './work_units_table';

const MOCK_WORK_UNITS_RESPONSE: ListWorkUnitsResponse = {
  workUnits: [
    {
      name: 'experiments/1/workUnits/1',
      tags: ['tag1'],
      createTime: '2023-10-27T10:00:00Z',
      currentWorkUnitExecution: {
        status: {
          state: 'RUNNING',
        },
        metadata: {
          mlrunUrl:
            'https://console.cloud.google.com/kubernetes/aiml/run/us-central1/test-run?project=xmc-test',
        },
      },
    },
    {
      name: 'experiments/1/workUnits/2',
      createTime: '2023-10-27T10:01:00Z',
      currentWorkUnitExecution: {
        status: {
          state: 'COMPLETED',
        },
        completeTime: '2023-10-27T11:01:00Z',
      },
      urls: [{ displayName: 'GKE Dashboard', url: 'http://gke-dashboard/2' }],
    },
    {
      name: 'experiments/1/workUnits/3',
      tags: ['tag2'],
      createTime: '2023-10-27T10:02:00Z',
      currentWorkUnitExecution: {
        status: {
          state: 'STOPPED',
        },
        completeTime: '2023-10-27T11:02:00Z',
      },
      urls: [{ displayName: 'GKE Dashboard', url: 'http://gke-dashboard/3' }],
    },
  ],
  nextPageToken: 'page2',
};

const EMPTY_WORK_UNITS_RESPONSE: ListWorkUnitsResponse = {
  workUnits: [],
};

describe('WorkUnitsTable Component', () => {
  const state = cleanState(() => {
    const workUnitsService = jasmine.createSpyObj<WorkUnitsService>('WorkUnitsService', [
      'searchWorkUnits',
    ]);
    const snackbarService = jasmine.createSpyObj<SnackbarService>('SnackbarService', ['open']);
    const filterService = jasmine.createSpyObj<StatusMessageFilterService>(
      'StatusMessageFilterService',
      ['isMessageVisible'],
      {
        severities$: new BehaviorSubject(new Set<StructuredMessage.Severity>()),
        sources$: new BehaviorSubject(new Set<StructuredMessage.Source>()),
      },
    );

    workUnitsService.searchWorkUnits.and.returnValue(of(MOCK_WORK_UNITS_RESPONSE));
    filterService.isMessageVisible.and.returnValue(true);

    setupModule({
      imports: [
        WorkUnitsTable,
        NoopAnimationsModule, // This makes test faster and more stable.
      ],
      providers: [
        provideRouter([]),
        { provide: WorkUnitsService, useValue: workUnitsService },
        { provide: SnackbarService, useValue: snackbarService },
        { provide: StatusMessageFilterService, useValue: filterService },
      ],
    });
    return { workUnitsService, snackbarService, filterService };
  }, beforeEach);

  it('should be created and display work units', async () => {
    const comp = bootstrap(WorkUnitsTable, { xid: '1' });
    expect(comp).toBeTruthy();

    const harness = await getHarness(WorkUnitsTableHarness);
    const rows = await harness.getDataRows();
    expect(rows.length).toBe(3);
    const cells = await rows[0].getCellTextByColumnName();
    expect(cells['ID']).toBe('1');
    expect(cells['Status']).toBe('Running');
    expect(cells['Tags']).toBe('tag1');
  });

  it('should display scheduling paused status', async () => {
    state.workUnitsService.searchWorkUnits.and.returnValue(
      of({
        workUnits: [
          {
            name: 'experiments/1/workUnits/4',
            createTime: '2023-10-27T10:03:00Z',
            currentWorkUnitExecution: { status: { state: 'SCHEDULING_PAUSED' } },
          },
        ],
      }),
    );
    bootstrap(WorkUnitsTable, { xid: '1' });

    const harness = await getHarness(WorkUnitsTableHarness);
    const rows = await harness.getDataRows();
    const cells = await rows[0].getCellTextByColumnName();
    expect(cells['Status']).toBe('Scheduling Paused');
  });

  it('should handle pagination', async () => {
    bootstrap(WorkUnitsTable, { xid: '1' });

    state.workUnitsService.searchWorkUnits.and.returnValue(
      of({ workUnits: [], nextPageToken: undefined }),
    );
    const nextButton = await getHarness(MatButtonHarness.with({ text: 'chevron_right' }));
    await nextButton.click();

    expect(state.workUnitsService.searchWorkUnits).toHaveBeenCalledWith({
      parent: 'experiments/1',
      pageSize: 10,
      pageToken: 'page2',
    });
  });

  describe('Loading states', () => {
    it('should show loading bar', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValue(NEVER);
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      expect(await harness.isLoading()).toBeTrue();
      expect(await harness.hasTable()).toBeFalse();
    });

    it('should show refreshing when has no work units', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValues(of(EMPTY_WORK_UNITS_RESPONSE), NEVER);
      const comp = bootstrap(WorkUnitsTable, { xid: '1' });
      comp.refresh();
      const harness = await getHarness(WorkUnitsTableHarness);
      expect(await harness.isRefreshing()).toBeTrue();
      expect(await harness.hasTable()).toBeTrue();
    });

    it('should show refreshing when has work units', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValues(of(MOCK_WORK_UNITS_RESPONSE), NEVER);
      const comp = bootstrap(WorkUnitsTable, { xid: '1' });
      comp.refresh();
      const harness = await getHarness(WorkUnitsTableHarness);
      expect(await harness.isRefreshing()).toBeTrue();
      expect(await harness.hasTable()).toBeTrue();
    });

    it('should hide loading bar when not fetching', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValue(of(MOCK_WORK_UNITS_RESPONSE));
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      expect(await harness.hasLoadingBar()).toBeFalse();
      expect(await harness.hasTable()).toBeTrue();
    });
  });

  describe('GKE Dashboard button', () => {
    it('should be enabled for running work unit', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      const rows = await harness.getDataRows();
      const cells = await rows[1].getCells();
      const button = await cells[cells.length - 1].getHarness(
        MatButtonHarness.with({ text: 'link' }),
      );

      expect(await button.isDisabled()).toBeFalse();
      const tooltip = await cells[cells.length - 1].getHarness(MatTooltipHarness);
      await tooltip.show();
      expect(await tooltip.getTooltipText()).toBe('GKE Dashboard');
    });

    it('should be disabled for stopped work unit', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      const rows = await harness.getDataRows();
      const cells = await rows[2].getCells();
      const button = await cells[cells.length - 1].getHarness(
        MatButtonHarness.with({ text: 'link' }),
      );

      expect(await button.isDisabled()).toBeTrue();
      const tooltip = await cells[cells.length - 1].getHarness(MatTooltipHarness);
      await tooltip.show();
      expect(await tooltip.getTooltipText()).toBe(
        'There are no GKE resources for stopped work units',
      );
    });
  });

  describe('Run Profile button', () => {
    it('should render run profile link for work unit with mlrunUrl', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      const button = await harness.getRunProfileButton(0);
      expect(button).not.toBeNull();

      const tooltip = await harness.getRunProfileTooltip(0);
      expect(tooltip).not.toBeNull();
      await tooltip!.show();
      expect(await tooltip!.getTooltipText()).toBe(
        'Access all profiles collected by the work unit in ML Diagnostics as well as capture on-demand profiles',
      );

      const link = await harness.getRunProfileLink(0);
      expect(link).toEqual({
        href: 'https://console.cloud.google.com/kubernetes/aiml/run/us-central1/test-run?project=xmc-test',
        target: '_blank',
        rel: 'noreferrer',
      });
    });

    it('should not render troubleshoot button for work unit without mlrunUrl', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      const button = await harness.getRunProfileButton(1);
      expect(button).toBeNull();
    });
  });

  describe('disableLinks', () => {
    it('should disable tag links by default', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      await getHarness(WorkUnitsTableHarness);

      const links = getEls('a.tag');
      expect(links.length).toBe(0);
    });
  });
});
