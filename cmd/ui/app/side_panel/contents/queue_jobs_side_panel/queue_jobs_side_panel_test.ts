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

import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { ResourceService } from 'xmanager_cloud/ui/app/service/resource_service';
import { bootstrap, cleanState, flush, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { QueueJobsSidePanel } from './queue_jobs_side_panel';

const TEST_QUEUE_JOBS_RESP = {
  queueName: 'gpu-cluster-queue',
  jobs: [
    {
      position: 'N/A-running',
      xid: '53296',
      workUnitId: '1',
      jobName: 'xmc-operation-53296-1',
      user: 'example@example.com',
      resources: 'GPU: 8, CPU: 32',
      priority: 'Low',
      status: 'Running',
      timeInQueue: '10m ago',
      queueName: 'gpu-cluster-queue',
    },
    {
      position: 'N/A-running',
      xid: '53308',
      workUnitId: '2',
      jobName: 'xmc-operation-53308-1',
      user: 'example-admin@example.com',
      resources: 'GPU: 8, CPU: 32',
      priority: 'High',
      status: 'Pending',
      timeInQueue: '5m ago',
      queueName: 'gpu-cluster-queue',
    },
  ],
};

describe('QueueJobsSidePanel Component', () => {
  const state = cleanState(() => {
    const resourceService = jasmine.createSpyObj<ResourceService>('ResourceService', [
      'getQueueJobs',
    ]);
    resourceService.getQueueJobs.and.returnValue(of(TEST_QUEUE_JOBS_RESP));

    setupModule({
      imports: [QueueJobsSidePanel, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: ResourceService, useValue: resourceService }],
    });

    return {
      resourceService,
    };
  }, beforeEach);

  it('should be created and load queue jobs', async () => {
    const comp = bootstrap(QueueJobsSidePanel, {
      inputs: { queueName: 'gpu-cluster-queue' },
    });
    await flush();

    expect(comp).toBeTruthy();
    expect(state.resourceService.getQueueJobs).toHaveBeenCalledWith('gpu-cluster-queue');
  });

  it('should properly render all table column headers', async () => {
    bootstrap(QueueJobsSidePanel, {
      inputs: { queueName: 'gpu-cluster-queue' },
    });
    await flush();

    const headerCells = Array.from(document.querySelectorAll('th.mat-mdc-header-cell')).map((th) =>
      th.textContent?.trim(),
    );
    expect(headerCells).toContain('Position');
    expect(headerCells).toContain('Experiment name');
    expect(headerCells).toContain('Status');
    expect(headerCells).toContain('Priority');
    expect(headerCells).toContain('Work / Aux Unit');
    expect(headerCells).toContain('Author');
  });

  it('should render correct row cell values for queue jobs', async () => {
    bootstrap(QueueJobsSidePanel, {
      inputs: { queueName: 'gpu-cluster-queue' },
    });
    await flush();

    const rows = document.querySelectorAll('tr.mat-mdc-row');
    expect(rows.length).toBe(2);

    const firstRow = rows[0];
    const posCell = firstRow.querySelector('.position-cell')?.textContent?.trim();
    const nameCell = firstRow.querySelector('.name-cell')?.textContent?.trim();
    const statusCell = firstRow.querySelector('.status-cell')?.textContent?.trim();
    const priorityCell = firstRow.querySelector('.priority-cell')?.textContent?.trim();
    const workunitCell = firstRow.querySelector('.workunit-cell')?.textContent?.trim();
    const authorCell = firstRow.querySelector('.author-cell')?.textContent?.trim();

    expect(posCell).toBe('N/A-running');
    expect(nameCell).toContain('xmc-operation-53296-1');
    expect(statusCell).toContain('Running');
    expect(priorityCell).toContain('Low');
    expect(workunitCell).toContain('1');
    expect(authorCell).toContain('example@example.com');

    const secondRow = rows[1];
    expect(secondRow.querySelector('.name-cell')?.textContent).toContain('xmc-operation-53308-1');
    expect(secondRow.querySelector('.status-cell')?.textContent).toContain('Pending');
    expect(secondRow.querySelector('.priority-cell')?.textContent).toContain('High');
    expect(secondRow.querySelector('.priority-badge')?.classList.contains('priority-high')).toBe(
      true,
    );
  });

  it('should display correct pagination info', async () => {
    bootstrap(QueueJobsSidePanel, {
      inputs: { queueName: 'gpu-cluster-queue' },
    });
    await flush();

    const paginationInfo = document.querySelector('.pagination-info')?.textContent?.trim();
    expect(paginationInfo).toBe('1-2 of 2');
  });
});
