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

    it('isStatusMessageHistoryEnabled should be false', () => {
      expect(state.service.isStatusMessageHistoryEnabled).toBeFalse();
    });

    it('isAdminPanelEnabled should be false', () => {
      expect(state.service.isAdminPanelEnabled).toBeFalse();
    });

    it('isResourceDashboardEnabled should be false', () => {
      expect(state.service.isResourceDashboardEnabled).toBeFalse();
    });

    it('isNonProdAuth should be false', () => {
      expect(state.service.isNonProdAuth).toBeFalse();
    });
  });

  describe('when XMC_FEATURE_FLAGS is defined', () => {
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

    describe('when adminPanelEnabled is true', () => {
      const state = setup({ adminPanelEnabled: true });

      it('isAdminPanelEnabled should be true if adminPanelEnabled is true', () => {
        expect(state.service.isAdminPanelEnabled).toBeTrue();
        expect(state.service.isFeatureEnabled('adminPanelEnabled')).toBeTrue();
      });
    });

    describe('when adminPanelEnabled is false', () => {
      const state = setup({ adminPanelEnabled: false });

      it('isAdminPanelEnabled should be false if adminPanelEnabled is false', () => {
        expect(state.service.isAdminPanelEnabled).toBeFalse();
        expect(state.service.isFeatureEnabled('adminPanelEnabled')).toBeFalse();
      });
    });

    describe('when resourceDashboardEnabled is true', () => {
      const state = setup({ resourceDashboardEnabled: true });

      it('isResourceDashboardEnabled should be true if resourceDashboardEnabled is true', () => {
        expect(state.service.isResourceDashboardEnabled).toBeTrue();
        expect(state.service.isFeatureEnabled('resourceDashboardEnabled')).toBeTrue();
      });
    });

    describe('when resourceDashboardEnabled is false', () => {
      const state = setup({ resourceDashboardEnabled: false });

      it('isResourceDashboardEnabled should be false if resourceDashboardEnabled is false', () => {
        expect(state.service.isResourceDashboardEnabled).toBeFalse();
        expect(state.service.isFeatureEnabled('resourceDashboardEnabled')).toBeFalse();
      });
    });

    describe('when isNonProdAuth is true', () => {
      const state = setup({ isNonProdAuth: true });

      it('isNonProdAuth should be true if isNonProdAuth is true', () => {
        expect(state.service.isNonProdAuth).toBeTrue();
        expect(state.service.isFeatureEnabled('isNonProdAuth')).toBeTrue();
      });
    });

    describe('when isNonProdAuth is false', () => {
      const state = setup({ isNonProdAuth: false });

      it('isNonProdAuth should be false if isNonProdAuth is false', () => {
        expect(state.service.isNonProdAuth).toBeFalse();
        expect(state.service.isFeatureEnabled('isNonProdAuth')).toBeFalse();
      });
    });
  });
});
