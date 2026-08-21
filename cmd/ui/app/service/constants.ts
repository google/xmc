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
import { InjectionToken } from '@angular/core';

declare global {
  interface Window {
    XMC_API_URL?: string;
  }
}

/** Base domain URL for XMC backend services (ESS, Health, ROD, etc.). */
export const XMC_API_DOMAIN =
  typeof window !== 'undefined' && window.XMC_API_URL != null ? window.XMC_API_URL : '/v1';

/** Whether to use the mock data or not. */
export const USE_MOCK_DATA = new InjectionToken<boolean>('USE_MOCK_DATA', {
  providedIn: 'root',
  factory: () => false,
});

/** Default refresh interval in milliseconds. */
export const DEFAULT_REFRESH_INTERVAL_MS = 30000; // 30 seconds

/**
 * Injection token for the auto-refresh interval in milliseconds.
 */
export const AUTO_REFRESH_INTERVAL_MS = new InjectionToken<number>('Auto-refresh interval ms');
