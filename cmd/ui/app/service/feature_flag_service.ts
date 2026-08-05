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
import { Injectable } from '@angular/core';

import { FeatureFlags } from './service_types';

/**
 * Service for checking whether a feature flag is enabled.
 */
@Injectable({ providedIn: 'root' })
export class FeatureFlagService {
  private readonly flags: FeatureFlags = window.XMC_FEATURE_FLAGS ?? {};

  /**
   * Returns whether the given feature flag is enabled.
   *
   * If the flag is not defined in FeatureFlags, it is considered disabled.
   */
  isFeatureEnabled(flagName: keyof FeatureFlags): boolean {
    return !!this.flags[flagName];
  }

  /**
   * Returns whether status message history is enabled.
   */
  get isStatusMessageHistoryEnabled(): boolean {
    return this.isFeatureEnabled('statusMessageHistoryEnabled');
  }

  /**
   * Returns whether the admin panel is enabled.
   */
  get isAdminPanelEnabled(): boolean {
    return this.isFeatureEnabled('adminPanelEnabled');
  }

  /**
   * Returns whether the resource dashboard is enabled.
   */
  get isResourceDashboardEnabled(): boolean {
    return this.isFeatureEnabled('resourceDashboardEnabled');
  }
}
