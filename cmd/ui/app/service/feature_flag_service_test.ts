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
import { cleanState, setupModule, get } from '../testing/test';
import 'jasmine';

import { FeatureFlagService } from './feature_flag_service';
import { FeatureFlags } from './service_types';

/**
 * Sets up the test. If featureFlags is not provided, it will be set to
 * undefined.
 */
function setup(featureFlags?: FeatureFlags) {
  return cleanState(() => {
    window.XMC_FEATURE_FLAGS = featureFlags;
    setupModule({
      providers: [FeatureFlagService],
    });
    return {
      service: get(FeatureFlagService),
    };
  }, beforeEach);
}

describe('FeatureFlagService', () => {
  describe('when XMC_FEATURE_FLAGS is undefined', () => {
    const state = setup();

    it('isCursorPaginationEnabled should be false', () => {
      expect(state.service.isCursorPaginationEnabled).toBeFalse();
    });

    it('isFeatureEnabled("cursorPaginationEnabled") should be false', () => {
      expect(state.service.isFeatureEnabled('cursorPaginationEnabled')).toBeFalse();
    });

    it('isStatusMessageHistoryEnabled should be false', () => {
      expect(state.service.isStatusMessageHistoryEnabled).toBeFalse();
    });
  });

  describe('when XMC_FEATURE_FLAGS is defined', () => {
    describe('when cursorPaginationEnabled is true', () => {
      const state = setup({ cursorPaginationEnabled: true });

      it('isCursorPaginationEnabled should be true if cursorPaginationEnabled is true', () => {
        expect(state.service.isCursorPaginationEnabled).toBeTrue();
        expect(state.service.isFeatureEnabled('cursorPaginationEnabled')).toBeTrue();
      });
    });

    describe('when cursorPaginationEnabled is false', () => {
      const state = setup({ cursorPaginationEnabled: false });

      it('isCursorPaginationEnabled should be false if cursorPaginationEnabled is false', () => {
        expect(state.service.isCursorPaginationEnabled).toBeFalse();
        expect(state.service.isFeatureEnabled('cursorPaginationEnabled')).toBeFalse();
      });
    });

    describe('when statusMessageHistoryEnabled is true', () => {
      const state = setup({ statusMessageHistoryEnabled: true });

      it('isStatusMessageHistoryEnabled should be true if statusMessageHistoryEnabled is true', () => {
        expect(state.service.isStatusMessageHistoryEnabled).toBeTrue();
        expect(state.service.isFeatureEnabled('statusMessageHistoryEnabled')).toBeTrue();
      });
    });

    describe('when statusMessageHistoryEnabled is false', () => {
      const state = setup({ statusMessageHistoryEnabled: false });

      it('isStatusMessageHistoryEnabled should be false if statusMessageHistoryEnabled is false', () => {
        expect(state.service.isStatusMessageHistoryEnabled).toBeFalse();
        expect(state.service.isFeatureEnabled('statusMessageHistoryEnabled')).toBeFalse();
      });
    });
  });
});
