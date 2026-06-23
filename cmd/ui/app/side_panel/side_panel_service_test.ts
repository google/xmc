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

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { BaseSidePanelData } from './base_side_panel';
import { SidePanelService } from './side_panel_service';

class MockSidePanelData implements BaseSidePanelData {
  constructor(readonly id: string) {}
}

describe('SidePanelService', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  const state = cleanState(() => {
    setupModule({ providers: [SidePanelService] });
    const service = get(SidePanelService);
    return { service };
  }, beforeEach);

  it('should be created', () => {
    expect(state.service).toBeTruthy();
  });

  describe('Initial State', () => {
    it('should initialize with an empty requests array', async () => {
      await expectAsync(state.service.dialogRequest$).toEmit(undefined);
    });
  });

  describe('dialogRequest$', () => {
    it('should emit undefined when no requests are present', async () => {
      state.service.closeSidePanel();

      await expectAsync(state.service.dialogRequest$).toEmit(undefined);
    });

    it('should emit the last request when one request is present', async () => {
      const request1 = new MockSidePanelData('req1');
      state.service.openSidePanel(request1);

      await expectAsync(state.service.dialogRequest$).toEmit(request1);
    });
  });

  describe('isPanelOpen$', () => {
    it('should emit true when a request is present', async () => {
      const request1 = new MockSidePanelData('req1');
      state.service.openSidePanel(request1);

      await expectAsync(state.service.isPanelOpen$).toEmit(true);
    });

    it('should emit false when no request is present', async () => {
      state.service.closeSidePanel();

      await expectAsync(state.service.isPanelOpen$).toEmit(false);
    });
  });

  describe('openSidePanel', () => {
    it('should set the request to the state', async () => {
      const request1 = new MockSidePanelData('req1');
      state.service.openSidePanel(request1);

      await expectAsync(state.service.dialogRequest$).toEmit(request1);
    });
  });

  describe('closeSidePanel', () => {
    it('should remove the request from the state', async () => {
      const request1 = new MockSidePanelData('req1');
      state.service.openSidePanel(request1);

      state.service.closeSidePanel();

      await expectAsync(state.service.dialogRequest$).toEmit(undefined);
    });
  });
});
