import {HttpClient, HttpParams} from '@angular/common/http';
import {inject, Injectable} from '@angular/core';
import {ListStatusMessagesRequest, ListStatusMessagesResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {MOCK_HISTORICAL_STATUS_MESSAGES} from 'xmanager_cloud/ui/app/common/mock_data';
import {Observable, of} from 'rxjs';

import {EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA} from './constants';

/**
 * Service to fetch status messages.
 */
@Injectable({providedIn: 'root'})
export class StatusMessageService {
  private readonly http = inject(HttpClient);
  private readonly useMockData = inject(USE_MOCK_DATA);

  /** List status messages for a given parent (experiment or work unit). */
  listStatusMessages(request: ListStatusMessagesRequest):
      Observable<ListStatusMessagesResponse> {
    if (this.useMockData) {
      return of({
        statusMessages: MOCK_HISTORICAL_STATUS_MESSAGES,
        nextPageToken: 'page_token',
      });
    }

    return this.listStatusMessagesPage(request);
  }

  private listStatusMessagesPage(request: ListStatusMessagesRequest):
      Observable<ListStatusMessagesResponse> {
    const {parent, pageSize, pageToken, filter, orderBy} = request;
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
    if (orderBy) {
      params = params.set('orderBy', orderBy);
    }

    return this.http.get<ListStatusMessagesResponse>(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/${parent}/statusMessages`,
        {params, withCredentials: true});
  }
}
