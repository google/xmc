// Copyright 2026 Google LLC
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

/** LocalStorage key storing the Resource Dashboard jobs full-width layout preference. */
export const JOBS_FULL_WIDTH_STORAGE_KEY = 'xmc-jobs-full-width';

/** Reads whether the jobs table full-width layout preference is enabled in localStorage. */
export function readJobsFullWidthPreference(
  storage: Pick<Storage, 'getItem'> | null | undefined = getBrowserLocalStorage(),
): boolean {
  try {
    return storage?.getItem(JOBS_FULL_WIDTH_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

/** Persists the jobs table full-width layout preference to localStorage. */
export function writeJobsFullWidthPreference(
  isFullWidth: boolean,
  storage: Pick<Storage, 'setItem'> | null | undefined = getBrowserLocalStorage(),
): void {
  try {
    storage?.setItem(JOBS_FULL_WIDTH_STORAGE_KEY, String(isFullWidth));
  } catch {
    // Ignore storage quota or security errors in restricted browser contexts.
  }
}

function getBrowserLocalStorage(): Storage | null {
  try {
    return typeof window !== 'undefined' ? window.localStorage : null;
  } catch {
    return null;
  }
}
