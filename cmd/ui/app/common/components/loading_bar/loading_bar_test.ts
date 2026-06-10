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
import { cleanState, setupModule, bootstrapTemplate, markForCheckAndFlush } from '../../../testing/test';
import 'jasmine';

import {getHarness} from '@angular/cdk/testing/catalyst/async';
import {NoopAnimationsModule} from '@angular/platform-browser/animations';

import {LoadingBar} from './loading_bar';
import {LoadingBarHarness} from './testing/loading_bar_harness';

describe('LoadingBar Component', () => {
  const state = cleanState(async () => {
    setupModule({
      imports: [LoadingBar, NoopAnimationsModule],
    });

    const instance = {isRefreshing: false};
    bootstrapTemplate(
        '<xmc-loading-bar [isRefreshing]="isRefreshing" />', instance);

    return {harness: getHarness(LoadingBarHarness), instance};
  }, beforeEach);

  it('should be created and display in buffer mode', async () => {
    const loadingBarHarness = await state.harness;

    expect(loadingBarHarness).toBeDefined();
    expect(await loadingBarHarness.getMode()).toBe('buffer');
  });

  it('should not be in refreshing mode by default', async () => {
    const loadingBarHarness = await state.harness;

    expect(await loadingBarHarness.isRefreshing()).toBeFalse();
  });

  it('should not be in refreshing mode when isRefreshing is false',
     async () => {
       const loadingBarHarness = await state.harness;

       expect(await loadingBarHarness.isRefreshing()).toBeFalse();
     });

  it('should be in refreshing mode when isRefreshing is true', async () => {
    const loadingBarHarness = await state.harness;
    state.instance.isRefreshing = true;
    await markForCheckAndFlush();

    expect(await loadingBarHarness.isRefreshing()).toBeTrue();
  });
});
