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
    },
    {
      positionInQueue: 2,
      xid: 53308,
      workUnitId: 2,
      user: 'example-admin@example.com',
      priority: 100,
      status: 'SCHEDULING',
      timeInQueueSeconds: 300,
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
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('gpu-cluster-queue');
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
    expect(firstRow.workUnitId).toContain('1');
    expect(firstRow.user).toContain('example@example.com');

    const secondRow = await harness.getRowCellsText(1);
    expect(secondRow.position).toBe('2');
    expect(secondRow.jobName).toContain('Experiment #53308');
    expect(secondRow.status).toContain('Scheduling');
    expect(secondRow.priority).toContain('100');
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
});
