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

import {getHarness} from '@angular/cdk/testing/catalyst/async';
import {MatButtonHarness} from '@angular/material/button/testing';
import {MatTableHarness} from '@angular/material/table/testing';
import {NoopAnimationsModule} from '@angular/platform-browser/animations';
import {ActivatedRoute, Params, provideRouter} from '@angular/router';
import {ListExperimentsResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {BehaviorSubject, of} from 'rxjs';

import {EmptyComponent} from '../common/testing_utils/empty_component';
import {AUTO_REFRESH_INTERVAL_MS, DEFAULT_REFRESH_INTERVAL_MS} from '../service/constants';
import {ExperimentService} from '../service/experiment_service';

import {ExperimentsListPage} from './experiments_list_page';

const MOCK_PAGE_RESULT_1: ListExperimentsResponse = {
  experiments: [
    {name: 'experiments/123'},
    {name: 'experiments/456'},
    {name: 'experiments/789'},
  ],
  nextPageToken: 'page_2',
};

const MOCK_PAGE_RESULT_2: ListExperimentsResponse = {
  experiments: [
    {name: 'experiments/123'},
    {name: 'experiments/456'},
    {name: 'experiments/789'},
  ],
};

describe('ExperimentsListPage Component', () => {
  useAutoTick();

  const state = cleanState(() => {
    const experimentService = jasmine.createSpyObj<ExperimentService>(
        'ExperimentService',
        ['searchExperiments'],
    );
    const queryParams = new BehaviorSubject<Params>({});
    const mockRoute = new ActivatedRoute();
    mockRoute.queryParams = queryParams;

    setupModule({
      imports: [
        ExperimentsListPage,
        NoopAnimationsModule,
      ],
      providers: [
        provideRouter([{path: 'experiments/:xid', component: EmptyComponent}]),
        {provide: ExperimentService, useValue: experimentService},
        {provide: ActivatedRoute, useFactory: () => mockRoute}, {
          provide: AUTO_REFRESH_INTERVAL_MS,
          useValue: DEFAULT_REFRESH_INTERVAL_MS,
        }
      ],
    });

    return {
      experimentService,
      queryParams,
    };
  }, beforeEach);

  it('should show a list of experiments', async () => {
    state.experimentService.searchExperiments.and.returnValues(
        of(MOCK_PAGE_RESULT_1),
    );

    bootstrap(ExperimentsListPage);

    const table = await getHarness(MatTableHarness);
    const rows = await table.getRows({selector: ':not(.detail-row)'});
    expect(rows.length).toBe(MOCK_PAGE_RESULT_1.experiments!.length);
  });

  it('should call the ExperimentService#searchExperiments correctly when the user clicks on the next page',
     async () => {
       state.experimentService.searchExperiments.and.returnValues(
           of(MOCK_PAGE_RESULT_1),
           of(MOCK_PAGE_RESULT_2),
       );

       bootstrap(ExperimentsListPage);

       const nextPageButton = await getHarness(
           MatButtonHarness.with(
               {selector: '[data-test-id="next-page-button"]'}),
       );
       await nextPageButton.click();

       expect(state.experimentService.searchExperiments).toHaveBeenCalledWith({
         filter: undefined,
         pageSize: 10,
         pageToken: 'page_2',
         orderBy: 'created desc',
       });
     });

  it('should call the ExperimentService#searchExperiments correctly when the user clicks on the previous page, given user is already on the second page',
     async () => {
       state.experimentService.searchExperiments.and.returnValues(
           of(MOCK_PAGE_RESULT_1),
           of(MOCK_PAGE_RESULT_2),
           of(MOCK_PAGE_RESULT_1),
       );

       bootstrap(ExperimentsListPage);

       const nextPageButton = await getHarness(
           MatButtonHarness.with(
               {selector: '[data-test-id="next-page-button"]'}),
       );
       await nextPageButton.click();

       const previousPageButton = await getHarness(
           MatButtonHarness.with(
               {selector: '[data-test-id="previous-page-button"]'}),
       );
       await previousPageButton.click();

       expect(state.experimentService.searchExperiments).toHaveBeenCalledWith({
         filter: undefined,
         pageSize: 10,
         pageToken: undefined,
         orderBy: 'created desc',
       });
     });

  it('should call the ExperimentService#searchExperiments correctly when the filter is set in the url',
     async () => {
       state.queryParams.next({filter: 'xid:123'});
       state.experimentService.searchExperiments.and.returnValues(
           of(MOCK_PAGE_RESULT_1),
       );

       bootstrap(ExperimentsListPage);

       expect(state.experimentService.searchExperiments).toHaveBeenCalledWith({
         filter: 'xid:123',
         pageSize: 10,
         pageToken: undefined,
         orderBy: 'created desc',
       });
     });
});
