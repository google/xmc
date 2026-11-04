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

import {
  JOBS_FULL_WIDTH_STORAGE_KEY,
  readJobsFullWidthPreference,
  writeJobsFullWidthPreference,
} from './jobs_layout_preference';

function createFakeStorage(
  initialEntries: Record<string, string> = {},
): Pick<Storage, 'getItem' | 'setItem'> & { readonly entries: Map<string, string> } {
  const entries = new Map<string, string>(Object.entries(initialEntries));
  return {
    entries,
    getItem(key: string): string | null {
      return entries.get(key) ?? null;
    },
    setItem(key: string, value: string): void {
      entries.set(key, value);
    },
  };
}

describe('jobs_layout_preference', () => {
  beforeEach(() => {
    window.localStorage.removeItem(JOBS_FULL_WIDTH_STORAGE_KEY);
  });

  afterEach(() => {
    window.localStorage.removeItem(JOBS_FULL_WIDTH_STORAGE_KEY);
  });

  describe('readJobsFullWidthPreference', () => {
    it('returns false when storage is empty', () => {
      const storage = createFakeStorage();
      expect(readJobsFullWidthPreference(storage)).toBeFalse();
    });

    it('returns false when xmc-jobs-full-width key is absent', () => {
      const storage = createFakeStorage({ 'xmc-other-pref': 'true' });
      expect(readJobsFullWidthPreference(storage)).toBeFalse();
    });

    it('returns true when xmc-jobs-full-width is set to true', () => {
      const storage = createFakeStorage({ [JOBS_FULL_WIDTH_STORAGE_KEY]: 'true' });
      expect(readJobsFullWidthPreference(storage)).toBeTrue();
    });

    it('returns false when xmc-jobs-full-width is set to false', () => {
      const storage = createFakeStorage({ [JOBS_FULL_WIDTH_STORAGE_KEY]: 'false' });
      expect(readJobsFullWidthPreference(storage)).toBeFalse();
    });

    it('returns false when storage is null or throws SecurityError', () => {
      expect(readJobsFullWidthPreference(null)).toBeFalse();
      const throwingStorage: Pick<Storage, 'getItem'> = {
        getItem(): string | null {
          throw new DOMException('Blocked', 'SecurityError');
        },
      };
      expect(readJobsFullWidthPreference(throwingStorage)).toBeFalse();
    });
  });

  describe('writeJobsFullWidthPreference', () => {
    it('writes true preference to storage', () => {
      const storage = createFakeStorage();
      writeJobsFullWidthPreference(true, storage);
      expect(storage.entries.get(JOBS_FULL_WIDTH_STORAGE_KEY)).toBe('true');
    });

    it('writes false preference to storage', () => {
      const storage = createFakeStorage({ [JOBS_FULL_WIDTH_STORAGE_KEY]: 'true' });
      writeJobsFullWidthPreference(false, storage);
      expect(storage.entries.get(JOBS_FULL_WIDTH_STORAGE_KEY)).toBe('false');
    });

    it('gracefully ignores null storage or QuotaExceededError', () => {
      expect(() => writeJobsFullWidthPreference(true, null)).not.toThrow();
      const throwingStorage: Pick<Storage, 'setItem'> = {
        setItem(): void {
          throw new DOMException('Quota exceeded', 'QuotaExceededError');
        },
      };
      expect(() => writeJobsFullWidthPreference(true, throwingStorage)).not.toThrow();
    });

    it('round-trips via default window.localStorage when storage argument is omitted', () => {
      expect(readJobsFullWidthPreference()).toBeFalse();
      writeJobsFullWidthPreference(true);
      expect(window.localStorage.getItem(JOBS_FULL_WIDTH_STORAGE_KEY)).toBe('true');
      expect(readJobsFullWidthPreference()).toBeTrue();
      writeJobsFullWidthPreference(false);
      expect(readJobsFullWidthPreference()).toBeFalse();
    });
  });
});
