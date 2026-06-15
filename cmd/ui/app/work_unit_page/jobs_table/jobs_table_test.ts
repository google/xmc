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

import {NoopAnimationsModule} from '@angular/platform-browser/animations';
import {StatusOption} from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';
import {WORK_UNIT_1} from 'xmanager_cloud/ui/app/common/mock_data';
import {bootstrap, setupModule} from 'xmanager_cloud/ui/app/testing/test';

import {JobsTable} from './jobs_table';

describe('JobsTable Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [JobsTable, NoopAnimationsModule],
    });
  });

  it('should be created', () => {
    const comp = bootstrap(JobsTable, {
      workUnit: WORK_UNIT_1,
    }) as JobsTable;
    expect(comp).toBeTruthy();
  });

  it('should compute replica displays correctly', () => {
    const comp = bootstrap(JobsTable, {
      workUnit: WORK_UNIT_1,
    }) as JobsTable;

    const displays = comp.getReplicaDisplays({
      succeeded: '5',
      failed: '2',
      active: '10',
      ready: '8',
    });

    expect(displays).toEqual([
      {status: StatusOption.SUCCESS, replicas: 5},
      {status: StatusOption.FAILED, replicas: 2},
      {status: StatusOption.RUNNING, replicas: 8},
      {status: StatusOption.PENDING, replicas: 2},
    ]);
  });
});
