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
import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';

import {
  WORK_UNIT_1,
  WORK_UNIT_2,
  WORK_UNIT_3,
  WORK_UNIT_4,
  WORK_UNIT_LIST,
} from 'app/common/mock_data';
import * as api from 'app/protos/experiment_state_server';
import { WorkUnit } from 'app/protos/experiment_state_server';
import { Operation } from 'app/protos/longrunning';

import { WorkUnitsService } from '../work_units_service';

/**
 * Mock implementation of WorkUnitsService for testing and mock data mode.
 */
@Injectable({ providedIn: 'root' })
export class MockWorkUnitsService extends WorkUnitsService {
  override searchWorkUnits(
    _request: api.ListWorkUnitsRequest,
  ): Observable<api.ListWorkUnitsResponse> {
    return of({
      workUnits: WORK_UNIT_LIST,
      nextPageToken: 'page_token',
    });
  }

  override getWorkUnit(request: api.GetWorkUnitRequest): Observable<WorkUnit> {
    const mockUnitsMap: Record<string, WorkUnit> = {
      'experiments/1/workUnits/1': WORK_UNIT_1,
      'experiments/1/workUnits/2': WORK_UNIT_2,
      'experiments/1/workUnits/3': WORK_UNIT_3,
      'experiments/1/workUnits/4': WORK_UNIT_4,
    };
    const result = mockUnitsMap[request.name] ?? WORK_UNIT_1;
    return of(result);
  }

  override stopWorkUnit(name: { xid: string; wid: string }): Observable<Operation> {
    return of({ name: `${name.xid}/${name.wid}`, done: true });
  }

  override restartWorkUnit(name: { xid: string; wid: string }): Observable<Operation> {
    return of({ name: `${name.xid}/${name.wid}`, done: true });
  }

  override batchStopWorkUnits(_xid: string): Observable<Operation> {
    return of({ done: true });
  }

  override batchRestartWorkUnits(_xid: string): Observable<Operation> {
    return of({ done: true });
  }
}
