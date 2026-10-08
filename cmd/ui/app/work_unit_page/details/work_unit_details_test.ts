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

import { provideRouter } from '@angular/router';

import { WORK_UNIT_1 } from 'xmanager_cloud/ui/app/common/mock_data';
import { bootstrap, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { WorkUnitDetails } from './work_unit_details';

describe('WorkUnitDetails Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [WorkUnitDetails],
      providers: [provideRouter([])],
    });
  });

  it('should be created', () => {
    const comp = bootstrap(WorkUnitDetails, {
      workUnit: WORK_UNIT_1,
    }) as WorkUnitDetails;
    expect(comp).toBeTruthy();
    expect(comp.workUnit()).toEqual(WORK_UNIT_1);
  });
});
