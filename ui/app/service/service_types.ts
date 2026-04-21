/**
 * Feature flags for the XManager UI.
 */
export interface FeatureFlags {
  cursorPaginationEnabled?: boolean;
  statusMessageHistoryEnabled?: boolean;
}

declare global {
  interface Window {
    // Global variable set by the python server, containing the feature flags.
    XMC_FEATURE_FLAGS?: FeatureFlags;
  }
}
