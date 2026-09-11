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

import { cleanState, get, setupModule } from '../testing/test';
import { FeatureFlags, FeatureFlagService } from './feature_flag_service';

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

const ALL_FLAGS: Array<{
  key: keyof FeatureFlags;
  getter: (service: FeatureFlagService) => boolean;
}> = [
  { key: 'adminPanelEnabled', getter: (s) => s.isAdminPanelEnabled },
  { key: 'resourceDashboardEnabled', getter: (s) => s.isResourceDashboardEnabled },
  { key: 'isNonProdAuth', getter: (s) => s.isNonProdAuth },
  { key: 'performanceDashboardEnabled', getter: (s) => s.isPerformanceDashboardEnabled },
  { key: 'pauseSchedulingEnabled', getter: (s) => s.isPauseSchedulingEnabled },
];

describe('FeatureFlagService', () => {
  describe('when XMC_FEATURE_FLAGS is undefined', () => {
    const state = setup();

    for (const { key, getter } of ALL_FLAGS) {
      it(`${key} should be false`, () => {
        expect(getter(state.service)).toBeFalse();
        expect(state.service.isFeatureEnabled(key)).toBeFalse();
      });
    }
  });

  describe('when XMC_FEATURE_FLAGS is defined', () => {
    for (const { key, getter } of ALL_FLAGS) {
      describe(`when ${key} is true`, () => {
        const state = setup({ [key]: true });

        it(`${key} should be true`, () => {
          expect(getter(state.service)).toBeTrue();
          expect(state.service.isFeatureEnabled(key)).toBeTrue();
        });
      });

      describe(`when ${key} is false`, () => {
        const state = setup({ [key]: false });

        it(`${key} should be false`, () => {
          expect(getter(state.service)).toBeFalse();
          expect(state.service.isFeatureEnabled(key)).toBeFalse();
        });
      });
    }
  });
});
