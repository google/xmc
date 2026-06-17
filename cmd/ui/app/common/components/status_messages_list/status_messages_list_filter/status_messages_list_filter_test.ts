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

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { StatusMessagesListFilterHarness } from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list_filter/testing/status_messages_list_filter_harness';
import { bootstrap, flush, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { StatusMessagesListFilter } from './status_messages_list_filter';

describe('StatusMessagesListFilter', () => {
  beforeEach(() => {
    setupModule({
      imports: [StatusMessagesListFilter, NoopAnimationsModule],
    });
  });

  it('should create', () => {
    const component = bootstrap(StatusMessagesListFilter);
    expect(component).toBeTruthy();
  });

  it('should emit severityFilterChange when severity changes', async () => {
    const component = bootstrap(StatusMessagesListFilter);
    let emitted: readonly string[] | undefined;
    component.severityFilterChange.subscribe((val) => {
      emitted = val;
    });

    const harness = await getHarness(StatusMessagesListFilterHarness);
    await harness.selectSeverity('Error');
    await flush();

    expect(emitted).toEqual(['ERROR']);
  });

  it('should emit sourceFilterChange when source changes', async () => {
    const component = bootstrap(StatusMessagesListFilter);
    let emitted: readonly string[] | undefined;
    component.sourceFilterChange.subscribe((val) => {
      emitted = val;
    });

    const harness = await getHarness(StatusMessagesListFilterHarness);
    await harness.selectSource('Application');
    await flush();

    expect(emitted).toEqual(['APPLICATION']);
  });

  it('should emit sortOrderChange when sort order changes', async () => {
    const component = bootstrap(StatusMessagesListFilter);
    let emitted: string | undefined;
    component.sortOrderChange.subscribe((val) => {
      emitted = val;
    });

    const harness = await getHarness(StatusMessagesListFilterHarness);
    await harness.selectSortOrder('Oldest First');
    await flush();

    expect(emitted).toEqual('ASC');
  });
});
