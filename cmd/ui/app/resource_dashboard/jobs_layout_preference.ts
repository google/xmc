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

/** Cookie name storing the Resource Dashboard jobs full-width layout preference. */
export const JOBS_FULL_WIDTH_COOKIE_NAME = 'xmc_jobs_full_width';

/** Max-Age in seconds (1 year) for the jobs full-width cookie. */
export const JOBS_FULL_WIDTH_COOKIE_MAX_AGE_SECONDS = 31536000;

/** Reads whether the jobs table full-width layout preference is enabled in document cookies. */
export function readJobsFullWidthCookie(doc: Document): boolean {
  const cookieHeader = doc.cookie || '';
  if (!cookieHeader) {
    return false;
  }
  for (const rawEntry of cookieHeader.split(';')) {
    const [rawKey, ...rawValueParts] = rawEntry.trim().split('=');
    if (rawKey === JOBS_FULL_WIDTH_COOKIE_NAME) {
      return rawValueParts.join('=') === 'true';
    }
  }
  return false;
}

/** Persists the jobs table full-width layout preference to document cookies. */
export function writeJobsFullWidthCookie(doc: Document, isFullWidth: boolean): void {
  doc.cookie = `${JOBS_FULL_WIDTH_COOKIE_NAME}=${isFullWidth}; Path=/; Max-Age=${JOBS_FULL_WIDTH_COOKIE_MAX_AGE_SECONDS}; SameSite=Lax`;
}
