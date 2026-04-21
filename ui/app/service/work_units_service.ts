import {HttpClient, HttpParams} from '@angular/common/http';
import {inject, Injectable} from '@angular/core';
import {Operation} from 'longrunning/operations.jsonpb_decls';
import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {WorkUnit} from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import {Observable, of} from 'rxjs';

import {WORK_UNIT_1, WORK_UNIT_2, WORK_UNIT_3, WORK_UNIT_4, WORK_UNIT_LIST} from '../common/mock_data';

import {EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA} from './constants';

/**
 * Service for fetching work units.
 */
@Injectable({providedIn: 'root'})
export class WorkUnitsService {
  private readonly http = inject(HttpClient);
  private readonly useMockData = inject(USE_MOCK_DATA);

  searchWorkUnits(request: api.ListWorkUnitsRequest):
      Observable<api.ListWorkUnitsResponse> {
    if (this.useMockData) {
      const result: api.ListWorkUnitsResponse = {
        workUnits: WORK_UNIT_LIST,
        nextPageToken: 'page_token',
      };
      return of(result);
    }

    const url =
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/${request.parent}/workUnits?`;
    const {pageSize, pageToken} = request;

    let params = new HttpParams();
    if (pageSize) {
      params = params.set('pageSize', pageSize);
    }
    if (pageToken) {
      params = params.set('pageToken', pageToken);
    }
    return this.http.get<api.ListWorkUnitsResponse>(
        url, {params, withCredentials: true});
  }

  getWorkUnit(request: api.GetWorkUnitRequest): Observable<WorkUnit> {
    if (this.useMockData) {
      let result: WorkUnit = WORK_UNIT_1;
      if (request.name === 'experiments/1/workUnits/1') {
        result = WORK_UNIT_1;
      } else if (request.name === 'experiments/1/workUnits/2') {
        result = WORK_UNIT_2;
      } else if (request.name === 'experiments/1/workUnits/3') {
        result = WORK_UNIT_3;
      } else if (request.name === 'experiments/1/workUnits/4') {
        result = WORK_UNIT_4;
      }
      return of(result);
    }

    const url = `${EXPERIMENT_STATE_SERVER_DOMAIN}/${request.name}`;
    return this.http.get<WorkUnit>(url, {withCredentials: true});
  }

  stopWorkUnit(name: {xid: string, wid: string}): Observable<Operation> {
    if (this.useMockData) {
      return of({name: name.xid + '/' + name.wid, done: true});
    }
    return this.http.post<Operation>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${name.xid}/workUnits/${
            name.wid}:stop`,
        {}, {withCredentials: true});
  }

  restartWorkUnit(name: {xid: string, wid: string}): Observable<Operation> {
    if (this.useMockData) {
      return of({name: name.xid + '/' + name.wid, done: true});
    }
    return this.http.post<Operation>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${name.xid}/workUnits/${
            name.wid}:restart`,
        {}, {withCredentials: true});
  }

  /**
   * Stops all work units in an experiment.
   */
  batchStopWorkUnits(xid: string): Observable<Operation> {
    if (this.useMockData) {
      const result = {done: true};
      return of(result);
    }

    return this.http.post<Operation>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${
            xid}/workUnits:batchStop`,
        null, {withCredentials: true});
  }

  /**
   * Restarts all work units in an experiment.
   */
  batchRestartWorkUnits(xid: string): Observable<Operation> {
    if (this.useMockData) {
      const result = {done: true};
      return of(result);
    }

    return this.http.post<Operation>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${
            xid}/workUnits:batchRestart`,
        null, {withCredentials: true});
  }
}
