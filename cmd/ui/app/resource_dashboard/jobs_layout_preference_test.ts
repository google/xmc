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
  JOBS_FULL_WIDTH_COOKIE_MAX_AGE_SECONDS,
  JOBS_FULL_WIDTH_COOKIE_NAME,
  readJobsFullWidthCookie,
  writeJobsFullWidthCookie,
} from './jobs_layout_preference';

function createFakeDocument(initialCookie = ''): Document {
  return { cookie: initialCookie } as unknown as Document;
}

describe('jobs_layout_preference', () => {
  describe('readJobsFullWidthCookie', () => {
    it('returns false when cookie header is empty', () => {
      const doc = createFakeDocument('');
      expect(readJobsFullWidthCookie(doc)).toBeFalse();
    });

    it('returns false when xmc_jobs_full_width cookie is absent', () => {
      const doc = createFakeDocument('theme=dark; other_pref=true');
      expect(readJobsFullWidthCookie(doc)).toBeFalse();
    });

    it('returns true when xmc_jobs_full_width is set to true', () => {
      const doc = createFakeDocument(`${JOBS_FULL_WIDTH_COOKIE_NAME}=true`);
      expect(readJobsFullWidthCookie(doc)).toBeTrue();
    });

    it('returns false when xmc_jobs_full_width is set to false', () => {
      const doc = createFakeDocument(`${JOBS_FULL_WIDTH_COOKIE_NAME}=false`);
      expect(readJobsFullWidthCookie(doc)).toBeFalse();
    });

    it('returns true when xmc_jobs_full_width=true is surrounded by other cookies', () => {
      const doc = createFakeDocument(
        `session_id=abc123; ${JOBS_FULL_WIDTH_COOKIE_NAME}=true; lang=en`,
      );
      expect(readJobsFullWidthCookie(doc)).toBeTrue();
    });
  });

  describe('writeJobsFullWidthCookie', () => {
    it('writes true preference with Path, Max-Age, and SameSite attributes', () => {
      const doc = createFakeDocument();
      writeJobsFullWidthCookie(doc, true);
      expect(doc.cookie).toBe(
        `${JOBS_FULL_WIDTH_COOKIE_NAME}=true; Path=/; Max-Age=${JOBS_FULL_WIDTH_COOKIE_MAX_AGE_SECONDS}; SameSite=Lax`,
      );
    });

    it('writes false preference with Path, Max-Age, and SameSite attributes', () => {
      const doc = createFakeDocument();
      writeJobsFullWidthCookie(doc, false);
      expect(doc.cookie).toBe(
        `${JOBS_FULL_WIDTH_COOKIE_NAME}=false; Path=/; Max-Age=${JOBS_FULL_WIDTH_COOKIE_MAX_AGE_SECONDS}; SameSite=Lax`,
      );
    });
  });
});
