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

import 'jasmine';

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { Component, signal } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { GetRunningAndPendingJobsResponse } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { bootstrap, cleanState, flush, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { QueueJobsTable } from './queue_jobs_table';
import { QueueJobsTableHarness } from './queue_jobs_table_harness';

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
      requestedResources: [{ resourceType: 'TPU v4', count: 16 }],
    },
  ],
  totalQueueSize: 2,
};

@Component({
  standalone: true,
  imports: [QueueJobsTable],
  template: `<xmc-queue-jobs-table [queueName]="queueName()" />`,
})
class TestHostComponent {
  readonly queueName = signal<string>('gpu-cluster-queue');
}

describe('QueueJobsTable Component', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    resourceService.getQueueJobs.and.returnValue(of(TEST_QUEUE_JOBS_RESP));

    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
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
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('gpu-cluster-queue', 10, '');
  });

  it('should properly render all table column headers using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    const headerCells = await harness.getHeaderCellTexts();
    expect(headerCells).toContain('Position');
    expect(headerCells).toContain('Experiment name');
    expect(headerCells).toContain('Status');
    expect(headerCells).toContain('Priority');
    expect(headerCells).toContain('Resources');
    expect(headerCells).toContain('Work / Aux Unit');
    expect(headerCells).toContain('Author');
  });

  it('should render correct row cell values for queue jobs using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(2);

    const firstRow = await harness.getRowCellsText(0);
    expect(firstRow.position).toBe('N/A - running');
    expect(firstRow.jobName).toContain('Experiment #53296');
    expect(firstRow.status).toContain('Running');
    expect(firstRow.priority).toContain('-10');
    expect(firstRow.resources).toContain('cpu: 3000000');
    expect(firstRow.resources).toContain('memory: 3298886726347889');
    expect(firstRow.resources).toContain('4 x H100');
    expect(firstRow.workUnitId).toContain('1');
    expect(firstRow.user).toContain('Work in Progress');

    const secondRow = await harness.getRowCellsText(1);
    expect(secondRow.position).toBe('2');
    expect(secondRow.jobName).toContain('Experiment #53308');
    expect(secondRow.status).toContain('Scheduling');
    expect(secondRow.priority).toContain('100');
    expect(secondRow.resources).toContain('16 x TPU v4');
  });

  it('should properly construct experiment and work unit router links using harness', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(2);

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

  it('should trigger server-side pagination when navigating to next page', async () => {
    const page1Jobs = Array.from({ length: 10 }, (_, i) => ({
      positionInQueue: i + 1,
      xid: 10000 + i,
      workUnitId: i + 1,
      user: `user${i}@example.com`,
      priority: 0,
      status: 'SCHEDULING',
      timeInQueueSeconds: 100,
      requestedResources: [{ resourceType: 'cpu', count: 1 }],
    }));
    const page2Jobs = Array.from({ length: 2 }, (_, i) => ({
      positionInQueue: 11 + i,
      xid: 10010 + i,
      workUnitId: 11 + i,
      user: `user${10 + i}@example.com`,
      priority: 0,
      status: 'SCHEDULING',
      timeInQueueSeconds: 100,
      requestedResources: [{ resourceType: 'cpu', count: 1 }],
    }));

    state.resourceService.getQueueJobs.and.callFake(
      (queueName: string, pageSize?: number, pageToken?: string) => {
        if (pageToken === 'page-2-token') {
          return of({ jobs: page2Jobs, totalQueueSize: 12, nextPageToken: '' });
        }
        return of({ jobs: page1Jobs, totalQueueSize: 12, nextPageToken: 'page-2-token' });
      },
    );

    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(QueueJobsTableHarness);
    expect(await harness.getRowCount()).toBe(10);
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('gpu-cluster-queue', 10, '');

    const paginator = await harness.getPaginator();
    expect(paginator).not.toBeNull();
    if (paginator) {
      await paginator.goToNextPage();
      await flush();
      expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith(
        'gpu-cluster-queue',
        10,
        'page-2-token',
      );
      expect(await harness.getRowCount()).toBe(2);
    }
  });
});
