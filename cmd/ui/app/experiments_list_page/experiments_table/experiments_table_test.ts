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
import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';

import { EmptyComponent } from 'app/common/testing_utils/empty_component';
import { ListExperimentsResponse } from 'app/protos/experiment_state_server';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';
import { bootstrapTemplate, cleanState, setupModule } from 'app/testing/test';

import { ExperimentsTable } from './experiments_table';
import { ExperimentsTableHarness } from './testing/experiments_table_harness';

const MOCK_EXPERIMENTS: ListExperimentsResponse = {
  experiments: [
    {
      name: 'experiments/12345',
      title: 'My First Experiment',
      tags: ['test', 'important'],
      status: { activeState: 'ACTIVE' },
      priority: 'HIGH',
      notes: 'Some important notes here.',
      resources: [],
      createTime: '2023-10-27T10:00:00Z',
      completeTime: '2023-10-27T11:00:00Z',
    },
    {
      name: 'experiments/67890',
      title: 'Another Experiment',
      tags: [],
      status: { activeState: 'INACTIVE' },
      priority: undefined,
      notes: 'Fewer details.',
      resources: undefined,
      createTime: '2023-10-28T12:00:00Z',
      completeTime: undefined,
    },
  ],
  nextPageToken: 'someToken',
};

const EMPTY_EXPERIMENTS: ListExperimentsResponse = {
  experiments: [],
};

interface TestContext {
  pageResult: ListExperimentsResponse | undefined;
  isFetching: boolean;
}

describe('ExperimentsTable Component', () => {
  async function setup(context: TestContext) {
    setupModule({
      imports: [ExperimentsTable, NoopAnimationsModule],
      providers: [provideRouter([{ path: 'experiments/:xid', component: EmptyComponent }])],
    });

    bootstrapTemplate(
      `<xmc-experiments-table [pageResult]="pageResult" [isFetching]="isFetching" />`,
      context,
    );
    return context;
  }

  it('should show no experiments message for empty experiments array', async () => {
    await setup({ pageResult: EMPTY_EXPERIMENTS, isFetching: false });
    const harness = await getHarness(ExperimentsTableHarness);
    expect(await harness.hasNoExperimentsMessage()).toBeTrue();
  });

  it('should render the table with correct number of rows when not fetching', async () => {
    await setup({ pageResult: MOCK_EXPERIMENTS, isFetching: false });
    const harness = await getHarness(ExperimentsTableHarness);
    const table = await harness.getTable();
    const rows = await table!.getRows({ selector: ':not(.detail-row)' });
    expect(rows.length).toBe(MOCK_EXPERIMENTS.experiments!.length);

    expect(await harness.hasLoadingBar()).toBeFalse();
    expect(await harness.hasNoExperimentsMessage()).toBeFalse();
  });

  describe('Status message layout', () => {
    const state = cleanState(async () => {
      setupModule({
        imports: [ExperimentsTable, NoopAnimationsModule],
        providers: [provideRouter([{ path: 'experiments/:xid', component: EmptyComponent }])],
      });
      const filters = TestBed.inject(StatusMessageFilterService);
      filters.severitiesSubject.next(new Set(['ERROR']));
      filters.sourcesSubject.next(new Set(['KUBERNETES']));

      const fixture = TestBed.createComponent(ExperimentsTable);
      fixture.nativeElement.style.cssText = 'display: block; width: 1400px';
      fixture.componentRef.setInput('pageResult', MOCK_EXPERIMENTS);
      fixture.componentRef.setInput('isFetching', false);
      const harness = await TestbedHarnessEnvironment.harnessForFixture(
        fixture,
        ExperimentsTableHarness,
      );
      return { fixture, harness };
    }, beforeEach);

    afterEach(() => state.fixture.destroy());

    for (const [name, message] of [
      ['long error text', 'Failed to create work unit: insufficient resources. '.repeat(80)],
      ['unbroken error text', `Failed to load /${'very_long_resource_name_'.repeat(200)}`],
    ]) {
      it(`should wrap ${name} without widening the table`, async () => {
        const originalWidth = await state.harness.getTableWidth();
        expect(originalWidth).toBeGreaterThan(0);

        const [experiment, ...otherExperiments] = MOCK_EXPERIMENTS.experiments!;
        state.fixture.componentRef.setInput('pageResult', {
          ...MOCK_EXPERIMENTS,
          experiments: [
            {
              ...experiment,
              status: {
                ...experiment.status,
                structuredStatusMessage: {
                  severity: 'ERROR',
                  source: 'KUBERNETES',
                  userFacingText: message,
                },
              },
            },
            ...otherExperiments,
          ],
        });

        expect(await state.harness.getErrorMessageText()).toBe(message.trim());
        expect(await state.harness.getTableWidth()).toBeLessThanOrEqual(originalWidth + 1);
        expect(await state.harness.getErrorMessageHorizontalOverflow()).toBeLessThanOrEqual(1);
      });
    }
  });

  describe('Loading states', () => {
    it('should show loading bar', async () => {
      await setup({ pageResult: undefined, isFetching: true });
      const harness = await getHarness(ExperimentsTableHarness);
      expect(await harness.isLoading()).toBeTrue();
      expect(await harness.isTablePresent()).toBeFalse();
    });

    it('should show refreshing when has no experiments', async () => {
      await setup({
        pageResult: EMPTY_EXPERIMENTS,
        isFetching: true,
      });
      const harness = await getHarness(ExperimentsTableHarness);
      expect(await harness.isRefreshing()).toBeTrue();
      expect(await harness.isTablePresent()).toBeTrue();
    });

    it('should show refreshing when has experiments', async () => {
      await setup({
        pageResult: MOCK_EXPERIMENTS,
        isFetching: true,
      });
      const harness = await getHarness(ExperimentsTableHarness);
      expect(await harness.isRefreshing()).toBeTrue();
      expect(await harness.isTablePresent()).toBeTrue();
    });

    it('should hide loading bar when not fetching', async () => {
      await setup({
        pageResult: MOCK_EXPERIMENTS,
        isFetching: false,
      });
      const harness = await getHarness(ExperimentsTableHarness);
      expect(await harness.hasLoadingBar()).toBeFalse();
      expect(await harness.isTablePresent()).toBeTrue();
    });
  });
});
