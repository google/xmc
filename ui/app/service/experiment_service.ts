import {HttpClient, HttpParams} from '@angular/common/http';
import {inject, Injectable} from '@angular/core';
import {ListExperimentsRequest, ListExperimentsResponse, UpdateExperimentRequest} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {EXPERIMENT_1, EXPERIMENT_LIST} from 'xmanager_cloud/ui/app/common/mock_data';
import {Observable, of, throwError} from 'rxjs';

import {EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA} from './constants';

/**
 * Service to fetch experiments.
 */
@Injectable({providedIn: 'root'})
export class ExperimentService {
  private readonly http = inject(HttpClient);
  private readonly useMockData = inject(USE_MOCK_DATA);

  /** Search for experiments. */
  searchExperiments(request: ListExperimentsRequest):
      Observable<ListExperimentsResponse> {
    if (!this.useMockData) {
      const {pageSize, pageToken, filter, orderBy} = request;
      let params = new HttpParams();
      if (pageSize) {
        params = params.set('pageSize', pageSize);
      }
      if (pageToken) {
        params = params.set('pageToken', pageToken);
      }
      if (filter) {
        params = params.set('filter', filter);
      }
      params = params.set('orderBy', orderBy || 'created desc');

      return this.http.get<ListExperimentsResponse>(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments`,
          {params, withCredentials: true});
    }
    if (!request.pageToken) {
      return of({
        experiments: EXPERIMENT_LIST.slice(0, 4),
        nextPageToken: 'page_2',
      });
    } else if (request.pageToken === 'page_2') {
      return of({
        experiments: EXPERIMENT_LIST.slice(3),
        nextPageToken: '',
      });
    } else {
      return throwError(() => new Error('Invalid page token'));
    }
  }

  /**
   * Fetch an experiment from the experiment state server.
   */
  getExperiment(xid: string): Observable<Experiment> {
    if (!this.useMockData) {
      return this.http.get<Experiment>(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${xid}`,
          {withCredentials: true});
    } else {
      const result = EXPERIMENT_LIST.find(
          experiment => experiment.name === `experiments/${xid}`);

      if (result) {
        return of(result);
      } else {
        return of(EXPERIMENT_1);
      }
    }
  }

  /**
   * Updates an experiment.
   * It returns a new experiment with updated fields.
   */
  updateExperiment(request: UpdateExperimentRequest): Observable<Experiment> {
    if (!this.useMockData) {
      let params = new HttpParams();
      if (request.updateMask) {
        params = params.set('updateMask', request.updateMask);
      }
      return this.http.patch<Experiment>(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/${request.experiment!.name}`,
          request.experiment, {params, withCredentials: true});
    }
    return of(request.experiment!);
  }
}
