import {InjectionToken} from '@angular/core';

declare global {
  interface Window {
    XMC_API_URL?: string;
  }
}

/** URL for the Experiment State Server. */
export const EXPERIMENT_STATE_SERVER_DOMAIN =
    window.XMC_API_URL || 'https://api.alpha.example.com/v1';

/** Whether to use the mock data or not. */
export const USE_MOCK_DATA = new InjectionToken<boolean>('USE_MOCK_DATA', {
  providedIn: 'root',
  factory: () => false,
});

/** Default refresh interval in milliseconds. */
export const DEFAULT_REFRESH_INTERVAL_MS = 30000;  // 30 seconds

/**
 * Injection token for the auto-refresh interval in milliseconds.
 */
export const AUTO_REFRESH_INTERVAL_MS = new InjectionToken<number>(
    'Auto-refresh interval ms',
);