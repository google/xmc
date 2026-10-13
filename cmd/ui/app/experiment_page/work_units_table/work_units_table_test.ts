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
import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatSelectHarness } from '@angular/material/select/testing';
import { MatTooltipHarness } from '@angular/material/tooltip/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { BehaviorSubject, NEVER, of, throwError } from 'rxjs';

import { ListWorkUnitsResponse, StructuredMessage } from 'app/protos/experiment_state_server';
import { SnackbarService } from 'app/service/snackbar_service';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';
import { WorkUnitsService } from 'app/service/work_units_service';
import { bootstrap, cleanState, getEls, setupModule } from 'app/testing/test';

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

  describe('Sorting', () => {
    const DEFAULT_REQUEST = { parent: 'experiments/1', pageSize: 10, pageToken: '' };

    function lastRequest() {
      return state.workUnitsService.searchWorkUnits.calls.mostRecent().args[0];
    }

    async function getStatusSortHeader(harness: WorkUnitsTableHarness) {
      const [header] = await (await harness.getSort()).getSortHeaders({ label: 'Status' });
      return header;
    }

    async function goToSecondPage(harness: WorkUnitsTableHarness) {
      await (await getHarness(MatButtonHarness.with({ text: 'chevron_right' }))).click();
      expect(await harness.getPageLabel()).toBe('Page 2');
    }

    it('should cycle the Status sort through asc, desc and cleared', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      await (await getStatusSortHeader(harness)).click();
      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state asc' });

      await (await getStatusSortHeader(harness)).click();
      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state desc' });

      await (await getStatusSortHeader(harness)).click();
      expect(lastRequest()).toEqual(DEFAULT_REQUEST);
    });

    it('should expose sort headers for ID, Status, Created and Completed only', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      const headers = await (await harness.getSort()).getSortHeaders();
      const labels = await Promise.all(headers.map((h) => h.getLabel()));
      expect(labels).toEqual(['ID', 'Status', 'Created', 'Completed']);
    });

    it('should reset to the first page when the sort changes', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      await goToSecondPage(harness);

      await (await getStatusSortHeader(harness)).click();

      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state asc' });
      expect(await harness.getPageLabel()).toBe('Page 1');
    });

    it('should keep the sort on refresh', async () => {
      const comp = bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      await (await getStatusSortHeader(harness)).click();

      comp.refresh();

      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state asc' });
    });

    it('should restore the active sort header after a failed fetch recovers', async () => {
      const comp = bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      state.workUnitsService.searchWorkUnits.and.returnValue(throwError(() => new Error('boom')));
      await (await getStatusSortHeader(harness)).click();
      expect(await harness.hasErrorMessage()).toBeTrue();

      state.workUnitsService.searchWorkUnits.and.returnValue(of(MOCK_WORK_UNITS_RESPONSE));
      comp.refresh();

      const activeHeader = await (await harness.getSort()).getActiveHeader();
      expect(await activeHeader?.getLabel()).toBe('Status');
      expect(await activeHeader?.getSortDirection()).toBe('asc');
      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state asc' });
    });

    it('should keep the sort headers usable after a failed fetch', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      state.workUnitsService.searchWorkUnits.and.returnValue(throwError(() => new Error('boom')));
      await (await getStatusSortHeader(harness)).click();
      expect(await harness.hasErrorMessage()).toBeTrue();
      expect((await (await harness.getSort()).getSortHeaders()).length).toBe(4);

      state.workUnitsService.searchWorkUnits.and.returnValue(of(MOCK_WORK_UNITS_RESPONSE));
      await (await getStatusSortHeader(harness)).click();

      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, orderBy: 'state desc' });
      expect(await harness.hasErrorMessage()).toBeFalse();
      expect((await harness.getDataRows()).length).toBe(3);
    });

    it('should keep the selected page size when the sort changes', async () => {
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);
      const pageSizeSelect = await getHarness(MatSelectHarness);
      await pageSizeSelect.open();
      await pageSizeSelect.clickOptions({ text: '25' });

      await (await getStatusSortHeader(harness)).click();

      expect(lastRequest()).toEqual({ ...DEFAULT_REQUEST, pageSize: 25, orderBy: 'state asc' });
    });

    it('should show only the empty message when the response has no work units', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValue(of(EMPTY_WORK_UNITS_RESPONSE));
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      expect(await harness.hasNoWorkUnitsMessage()).toBeTrue();
      expect(await harness.hasErrorMessage()).toBeFalse();
    });

    it('should show only the error message when the fetch fails', async () => {
      state.workUnitsService.searchWorkUnits.and.returnValue(throwError(() => new Error('boom')));
      bootstrap(WorkUnitsTable, { xid: '1' });
      const harness = await getHarness(WorkUnitsTableHarness);

      expect(await harness.hasErrorMessage()).toBeTrue();
      expect(await harness.hasNoWorkUnitsMessage()).toBeFalse();
    });
  });
});
