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
import { HttpClient, HttpParams } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Operation } from 'longrunning/operations.jsonpb_decls';
import { Observable } from 'rxjs';

import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { WorkUnit } from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';

import { XMC_API_DOMAIN } from './constants';

/**
 * Service for fetching work units.
 */
@Injectable({ providedIn: 'root' })
export class WorkUnitsService {
  private readonly http = inject(HttpClient);

  searchWorkUnits(request: api.ListWorkUnitsRequest): Observable<api.ListWorkUnitsResponse> {
    const url = `${XMC_API_DOMAIN}/${request.parent}/workUnits`;
    const { pageSize, pageToken } = request;

    const params = new HttpParams({
      fromObject: Object.fromEntries(
        Object.entries({ pageSize, pageToken }).filter(([_, v]) => Boolean(v)),
      ),
    });
    return this.http.get<api.ListWorkUnitsResponse>(url, { params, withCredentials: true });
  }

  getWorkUnit(request: api.GetWorkUnitRequest): Observable<WorkUnit> {
    const url = `${XMC_API_DOMAIN}/${request.name}`;
    return this.http.get<WorkUnit>(url, { withCredentials: true });
  }

  stopWorkUnit(name: { xid: string; wid: string }): Observable<Operation> {
    return this.http.post<Operation>(
      `${XMC_API_DOMAIN}/experiments/${name.xid}/workUnits/${name.wid}:stop`,
      {},
      { withCredentials: true },
    );
  }

  restartWorkUnit(name: { xid: string; wid: string }): Observable<Operation> {
    return this.http.post<Operation>(
      `${XMC_API_DOMAIN}/experiments/${name.xid}/workUnits/${name.wid}:restart`,
      {},
      { withCredentials: true },
    );
  }

  /**
   * Stops all work units in an experiment.
   */
  batchStopWorkUnits(xid: string): Observable<Operation> {
    return this.http.post<Operation>(
      `${XMC_API_DOMAIN}/experiments/${xid}/workUnits:batchStop`,
      null,
      { withCredentials: true },
    );
  }

  /**
   * Restarts all work units in an experiment.
   */
  batchRestartWorkUnits(xid: string): Observable<Operation> {
    return this.http.post<Operation>(
      `${XMC_API_DOMAIN}/experiments/${xid}/workUnits:batchRestart`,
      null,
      { withCredentials: true },
    );
  }
}
