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

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { Component, signal } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of, Subject, throwError } from 'rxjs';

import { GetRunningAndPendingJobsResponse } from 'app/protos/rod';
import { ResourceService } from 'app/service/resource_service';
import { bootstrap, cleanState, flush, setupModule } from 'app/testing/test';

import { QueueJobsTable } from './queue_jobs_table';
import { QueueJobsTableHarness } from './queue_jobs_table_harness';

const EMPTY_STATE_SELECTOR = '.jobs-empty-state';
const LOADING_CONTAINER_SELECTOR = '.loading-container';

const TEST_QUEUE_JOBS_RESP: GetRunningAndPendingJobsResponse = {
  jobs: [
    {
      positionInQueue: 1,
      xid: 53296,
      workUnitId: 1,
      user: 'example@example.com',
      priority: -10,
      status: 'RUNNING',
      timeInQueueSeconds: 600,
      runningDurationSeconds: 51720,
      experimentTitle: 'Fine-Tuning Llama',
      requestedResources: [
        { resourceType: 'cpu', count: 3000000 },
        { resourceType: 'memory', count: 3298886726347889 },
        { resourceType: 'H100', count: 4 },
      ],
    },
    {
      positionInQueue: 2,
      xid: 53308,
      workUnitId: 2,
      user: 'example-admin@example.com',
      priority: 100,
      status: 'SCHEDULING',
      timeInQueueSeconds: 300,
      experimentTitle: '',
      requestedResources: [{ resourceType: 'TPU v4', count: 16 }],
    },
    {
      positionInQueue: 3,
      xid: 53310,
      workUnitId: 3,
      priority: 0,
      status: 'SCHEDULING',
      timeInQueueSeconds: 150,
      requestedResources: [{ resourceType: 'cpu', count: 1000 }],
    },
  ],
  totalQueueSize: 3,
  userAllocations: [
    {
      user: 'example@example.com',
      resourceType: 'nvidia.com/gpu: H100',
      runningUnits: 4,
    },
    {
      user: 'example-admin@example.com',
      resourceType: 'nvidia.com/gpu: H100',
      runningUnits: 2,
    },
  ],
};

@Component({
  standalone: true,
  imports: [QueueJobsTable],
  template: `<xmc-queue-jobs-table
    [queueName]="queueName()"
    [dominantResource]="dominantResource()"
  />`,
})
class TestHostComponent {
  readonly queueName = signal<string>('gpu-cluster-queue');
  readonly dominantResource = signal<string | null>(null);
}

@Component({
  standalone: true,
  imports: [QueueJobsTable],
  template: `
    <xmc-queue-jobs-table [queueName]="'gpu-cluster-queue'">
      <button headerActions type="button">Custom header action</button>
      <span>Unmatched content</span>
    </xmc-queue-jobs-table>
  `,
})
class ProjectedHeaderActionsHostComponent {}

describe('QueueJobsTable Component', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    resourceService.getQueueJobs.and.returnValue(of(TEST_QUEUE_JOBS_RESP));

    setupModule({
      imports: [TestHostComponent, ProjectedHeaderActionsHostComponent, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: ResourceService, useValue: resourceService }],
    });

    return {
      resourceService,
    };
  }, beforeEach);

  it('should be created and load queue jobs', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    expect(host).toBeTruthy();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      '',
    );
  });

  it('requests ascending, descending, and default ordering through the headers', async () => {
    bootstrap(TestHostComponent);
    await flush();
    const harness = await getHarness(QueueJobsTableHarness);
    for (const [label, field] of [
      ['Author', 'author'],
      ['Status', 'status'],
      ['Priority', 'priority'],
    ]) {
      for (const direction of ['asc', 'desc', '']) {
        state.resourceService.getQueueJobs.calls.reset();
        await harness.sortBy(label);
        await flush();
        expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
          'gpu-cluster-queue',
          10,
          '',
          direction ? `${field} ${direction}` : '',
          '',
        );
      }
    }
  });

  it('resets pagination when the sort changes', async () => {
    state.resourceService.getQueueJobs.and.returnValue(
      of({
        ...TEST_QUEUE_JOBS_RESP,
        totalQueueSize: 30,
        nextPageToken: 'second-page',
      }),
    );
    bootstrap(TestHostComponent);
    await flush();
    const harness = await getHarness(QueueJobsTableHarness);
    const paginator = await harness.getPaginator();
    await paginator!.goToNextPage();
    await flush();
    expect(await paginator!.getRangeLabel()).toMatch(/^11\s/);
    state.resourceService.getQueueJobs.calls.reset();
    await harness.sortBy('Author');
    await flush();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
      'gpu-cluster-queue',
      10,
      '',
      'author asc',
      '',
    );
    expect(await paginator!.getRangeLabel()).toMatch(/^1\s/);
  });

  it('combines author and status filters and clears both', async () => {
    bootstrap(TestHostComponent);
    await flush();
    const harness = await getHarness(QueueJobsTableHarness);
    await harness.filterAuthor('alice@example.com');
    await harness.filterStatus('RUNNING');
    await flush();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      'author = "alice@example.com" AND status = RUNNING',
    );
    state.resourceService.getQueueJobs.calls.reset();
    await harness.clearFilters();
    await flush();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      '',
    );
  });

  it('resets pagination when author or status changes', async () => {
    state.resourceService.getQueueJobs.and.returnValue(
      of({
        ...TEST_QUEUE_JOBS_RESP,
        totalQueueSize: 30,
        nextPageToken: 'second-page',
      }),
    );
    bootstrap(TestHostComponent);
    await flush();
    const harness = await getHarness(QueueJobsTableHarness);
    const paginator = await harness.getPaginator();
    await paginator!.goToNextPage();
    await harness.filterAuthor('alice@example.com');
    await flush();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      'author = "alice@example.com"',
    );
    await paginator!.goToNextPage();
    await harness.filterStatus('RUNNING');
    await flush();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      'author = "alice@example.com" AND status = RUNNING',
    );
  });

  it('should properly render all table column headers using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    const headerCells = await harness.getHeaderCellTexts();
    expect(headerCells).toContain('Position');
    expect(headerCells).toContain('Experiment name');
    expect(headerCells).toContain('Status');
    expect(headerCells).toContain('Running time');
    expect(headerCells).toContain('Priority');
    expect(headerCells).toContain('Resources');
    expect(headerCells).toContain('Work / Aux Unit');
    expect(headerCells).toContain('Author');

    const runningTimeTooltip = await harness.getRunningTimeTooltip();
    expect(runningTimeTooltip).not.toBeNull();
    await runningTimeTooltip!.show();
    const tooltipText = await runningTimeTooltip!.getTooltipText();
    expect(tooltipText).toContain('observed the job entering the RUNNING state');
    expect(tooltipText).toContain('Resets whenever the job leaves RUNNING');
  });

  it('should render correct row cell values for queue jobs using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(3);

    const firstRow = await harness.getRowCellsText(0);
    expect(firstRow.position).toBe('N/A - running');
    expect(firstRow.jobName).toContain('Fine-Tuning Llama');
    expect((await harness.getRowLinks(0)).jobLinkAriaLabel).toBe('View Fine-Tuning Llama');
    expect(firstRow.status).toContain('Running');
    expect(firstRow.runningTime).toBe('14h 22m');
    expect(firstRow.priority).toContain('-10');
    expect(firstRow.resources).toContain('cpu: 3000000');
    expect(firstRow.resources).toContain('memory: 3298886726347889');
    expect(firstRow.resources).toContain('4 x H100');
    expect(firstRow.workUnitId).toContain('1');
    expect(firstRow.user).toContain('example@example.com');

    const secondRow = await harness.getRowCellsText(1);
    expect(secondRow.position).toBe('2');
    expect(secondRow.jobName).toContain('Experiment #53308');
    expect((await harness.getRowLinks(1)).jobLinkAriaLabel).toBe('View Experiment #53308');
    expect(secondRow.status).toContain('Scheduling');
    expect(secondRow.runningTime).toBe('–');
    expect(secondRow.priority).toContain('100');
    expect(secondRow.resources).toContain('16 x TPU v4');
    expect(secondRow.user).toContain('example-admin@example.com');

    const thirdRow = await harness.getRowCellsText(2);
    expect(thirdRow.position).toBe('3');
    expect(thirdRow.jobName).toContain('Experiment #53310');
    expect((await harness.getRowLinks(2)).jobLinkAriaLabel).toBe('View Experiment #53310');
    expect(thirdRow.status).toContain('Scheduling');
    expect(thirdRow.runningTime).toBe('–');
    expect(thirdRow.priority).toContain('0');
    expect(thirdRow.resources).toContain('cpu: 1000');
    expect(thirdRow.user).toBe('-');
  });

  it('should properly construct experiment and work unit router links using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(3);

    const firstRowLinks = await harness.getRowLinks(0);
    expect(firstRowLinks.jobLink).toBe('/experiments/53296');
    expect(firstRowLinks.workUnitLink).toBe('/experiments/53296/workUnits/1');

    const secondRowLinks = await harness.getRowLinks(1);
    expect(secondRowLinks.jobLink).toBe('/experiments/53308');
    expect(secondRowLinks.workUnitLink).toBe('/experiments/53308/workUnits/2');
  });

  it('should render mat-paginator when jobs exist', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    const paginator = await harness.getPaginator();
    expect(paginator).not.toBeNull();
    if (paginator) {
      expect(await paginator.getPageSize()).toBe(10);
    }
  });

  it('should handle server-side pagination navigation', async () => {
    state.resourceService.getQueueJobs.and.callFake(
      (_queueName: string, _pageSize?: number, pageToken?: string) => {
        if (pageToken === 'token-2') {
          return of({
            jobs: [{ positionInQueue: 11, xid: 10011, workUnitId: 11, status: 'SCHEDULING' }],
            totalQueueSize: 11,
            nextPageToken: '',
          });
        }
        return of({
          jobs: [{ positionInQueue: 1, xid: 10001, workUnitId: 1, status: 'SCHEDULING' }],
          totalQueueSize: 11,
          nextPageToken: 'token-2',
        });
      },
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    const paginator = await harness.getPaginator();
    expect(paginator).not.toBeNull();
    if (paginator) {
      // Forward
      state.resourceService.getQueueJobs.calls.reset();
      await paginator.goToNextPage();
      await flush();
      expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
        'gpu-cluster-queue',
        10,
        'token-2',
        '',
        '',
      );

      // Backward
      state.resourceService.getQueueJobs.calls.reset();
      await paginator.goToPreviousPage();
      await flush();
      expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
        'gpu-cluster-queue',
        10,
        '',
        '',
        '',
      );

      // Page size change
      state.resourceService.getQueueJobs.calls.reset();
      await paginator.setPageSize(20);
      await flush();
      expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
        'gpu-cluster-queue',
        20,
        '',
        '',
        '',
      );
    }
  });

  it('should reset pagination when active queue changes', async () => {
    const hostComponent = bootstrap(TestHostComponent);
    await flush();

    hostComponent.queueName.set('tpu-cluster-queue');
    await flush();

    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
      'tpu-cluster-queue',
      10,
      '',
      '',
      '',
    );
  });

  it('should handle API errors gracefully', async () => {
    state.resourceService.getQueueJobs.and.returnValue(
      throwError(() => new Error('Network error')),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(0);
  });

  it('should display empty state when queue has no jobs', async () => {
    state.resourceService.getQueueJobs.and.returnValue(
      of({ jobs: [], totalQueueSize: 0, nextPageToken: '' }),
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(0);

    const paginator = await harness.getPaginator();
    expect(paginator).toBeNull();

    expect(document.querySelector(EMPTY_STATE_SELECTOR)).not.toBeNull();
    expect(document.body.textContent).toContain('No matching jobs.');
  });

  it('should display loading state while fetching jobs', async () => {
    const jobsSubject = new Subject<GetRunningAndPendingJobsResponse>();
    state.resourceService.getQueueJobs.and.returnValue(jobsSubject.asObservable());

    bootstrap(TestHostComponent);

    expect(document.querySelector(LOADING_CONTAINER_SELECTOR)).not.toBeNull();
    expect(document.body.textContent).toContain('Loading jobs...');

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getPaginator()).toBeNull();
    expect(await harness.getUserAllocationBar()).toBeNull();

    jobsSubject.next(TEST_QUEUE_JOBS_RESP);
    await flush();

    expect(document.querySelector(LOADING_CONTAINER_SELECTOR)).toBeNull();
    expect(await harness.getRowCount()).toBe(3);
  });

  it('keeps the sort table mounted while a sort request is pending', async () => {
    const jobsSubject = new Subject<GetRunningAndPendingJobsResponse>();
    state.resourceService.getQueueJobs.and.returnValue(jobsSubject.asObservable());

    bootstrap(TestHostComponent);
    jobsSubject.next(TEST_QUEUE_JOBS_RESP);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    await harness.sortBy('Author');

    expect(document.querySelector(LOADING_CONTAINER_SELECTOR)).toBeNull();
    expect(document.querySelector('xmc-loading-bar')).not.toBeNull();
    expect(await harness.getRowCount()).toBe(3);

    jobsSubject.next(TEST_QUEUE_JOBS_RESP);
    await flush();
    expect(document.querySelector('xmc-loading-bar')).toBeNull();
  });

  it('should log a console.error when a job is missing an assigned user', async () => {
    spyOn(console, 'error');
    bootstrap(TestHostComponent);
    await flush();

    expect(console.error).toHaveBeenCalledWith(
      'Job "Experiment #53310" is missing an assigned user.',
    );
  });

  it('should render no header actions when [headerActions] content is not projected', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getHeaderActionTexts()).toEqual([]);
  });

  it('should project only [headerActions] elements into the title row', async () => {
    bootstrap(ProjectedHeaderActionsHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getHeaderActionTexts()).toEqual(['Custom header action']);
    expect(document.body.textContent).not.toContain('Unmatched content');
  });

  it('filters by author when clicking a user allocation chip and toggles off on second click', async () => {
    const host = bootstrap(TestHostComponent);
    host.dominantResource.set('nvidia.com/gpu: H100');
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    const bar = (await harness.getUserAllocationBar())!;
    state.resourceService.getQueueJobs.calls.reset();

    await bar.clickUserChip('example@example.com');
    await flush();

    expect(await bar.isUserChipSelected('example@example.com')).toBeTrue();
    expect(await harness.getAuthorFilterValue()).toBe('example@example.com');
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      'author = "example@example.com"',
    );

    // Clicking a different chip switches the author filter and updates the Author input
    state.resourceService.getQueueJobs.calls.reset();
    await bar.clickUserChip('example-admin@example.com');
    await flush();

    expect(await bar.isUserChipSelected('example-admin@example.com')).toBeTrue();
    expect(await bar.isUserChipSelected('example@example.com')).toBeFalse();
    expect(await harness.getAuthorFilterValue()).toBe('example-admin@example.com');
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      'author = "example-admin@example.com"',
    );

    // Clicking the same chip again toggles/clears the author filter
    state.resourceService.getQueueJobs.calls.reset();
    await bar.clickUserChip('example-admin@example.com');
    await flush();

    expect(await bar.isUserChipSelected('example-admin@example.com')).toBeFalse();
    expect(await harness.getAuthorFilterValue()).toBe('');
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledOnceWith(
      'gpu-cluster-queue',
      10,
      '',
      '',
      '',
    );
  });
});
