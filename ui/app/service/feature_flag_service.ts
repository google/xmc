import {Injectable} from '@angular/core';

import {FeatureFlags} from './service_types';

/**
 * Service for checking whether a feature flag is enabled.
 */
@Injectable({providedIn: 'root'})
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
   * Returns whether cursor pagination is enabled.
   */
  get isCursorPaginationEnabled(): boolean {
    return this.isFeatureEnabled('cursorPaginationEnabled');
  }

  /**
   * Returns whether status message history is enabled.
   */
  get isStatusMessageHistoryEnabled(): boolean {
    return this.isFeatureEnabled('statusMessageHistoryEnabled');
  }
}