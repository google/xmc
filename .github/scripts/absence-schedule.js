// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

const USERNAME_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9_-]*[A-Za-z0-9])?$/;
const DATE_PATTERN = /^(\d{2})\.(\d{2})\.(\d{4})$/;
const DATE_RANGE_PATTERN = /^([^-\s]+)-([^-\s]+)$/;

/**
 * Helper: Builds a failed line-parse result with a human-readable reason.
 * @param {string} reason - Validation failure message.
 * @returns {{ empty: false, valid: false, reason: string }}
 */
function invalidEntry(reason) {
  return { empty: false, valid: false, reason };
}

/**
 * Pure function: Strictly parses a DD.MM.YYYY calendar date string in UTC.
 * Rejects impossible calendar dates (e.g., 31.02.2026 or non-leap 29.02.2026).
 * @param {string} dateStr - Date string in DD.MM.YYYY format.
 * @returns {Date|null} Parsed UTC midnight Date or null if invalid.
 */
function parseDdMmYyyy(dateStr) {
  const match = (dateStr || '').match(DATE_PATTERN);
  if (!match) {
    return null;
  }
  const day = parseInt(match[1], 10);
  const month = parseInt(match[2], 10);
  const year = parseInt(match[3], 10);
  const date = new Date(Date.UTC(year, month - 1, day));
  date.setUTCFullYear(year, month - 1, day);
  if (
    date.getUTCFullYear() !== year ||
    date.getUTCMonth() !== month - 1 ||
    date.getUTCDate() !== day
  ) {
    return null;
  }
  return date;
}

/**
 * Pure function: Parses and validates a DD.MM.YYYY-DD.MM.YYYY date range token.
 * @param {string} rangeStr - Date range token.
 * @param {number} lineNumber - 1-based line number for error reporting.
 * @returns {{ empty: false, valid: boolean, startDate?: Date, endDate?: Date, reason?: string }}
 */
function parseAbsenceDateRange(rangeStr, lineNumber) {
  const match = (rangeStr || '').match(DATE_RANGE_PATTERN);
  if (!match) {
    return invalidEntry(
      `Line ${lineNumber} has invalid date range format "${rangeStr}" (expected DD.MM.YYYY-DD.MM.YYYY)`
    );
  }
  const startDate = parseDdMmYyyy(match[1]);
  if (!startDate) {
    return invalidEntry(
      `Line ${lineNumber} has invalid start date "${match[1]}" (expected valid DD.MM.YYYY calendar date)`
    );
  }
  const endDate = parseDdMmYyyy(match[2]);
  if (!endDate) {
    return invalidEntry(
      `Line ${lineNumber} has invalid end date "${match[2]}" (expected valid DD.MM.YYYY calendar date)`
    );
  }
  if (startDate > endDate) {
    return invalidEntry(
      `Line ${lineNumber} has start date "${match[1]}" after end date "${match[2]}"`
    );
  }
  return { empty: false, valid: true, startDate, endDate };
}

/**
 * Pure function: Parses and validates a single line from ABSENCE.md.
 * Supports either bare "NICK" or "NICK DD.MM.YYYY-DD.MM.YYYY" (inclusive).
 * @param {string} rawLine - Raw line from ABSENCE.md.
 * @param {number} [lineNumber=1] - 1-based line number for error reporting.
 * @returns {{ empty: boolean, valid: boolean, username?: string, startDate?: Date|null, endDate?: Date|null, reason?: string }}
 */
function parseAbsenceLine(rawLine, lineNumber = 1) {
  const cleaned = (rawLine || '').split('#')[0].trim().replace(/^[-*+@\s]+/, '').trim();
  if (!cleaned) {
    return { empty: true, valid: true };
  }
  const parts = cleaned.split(/\s+/);
  if (parts.length > 2) {
    return invalidEntry(
      `Line ${lineNumber} has invalid format "${cleaned}" (expected "NICK" or "NICK DD.MM.YYYY-DD.MM.YYYY")`
    );
  }
  const username = parts[0];
  if (!USERNAME_PATTERN.test(username)) {
    return invalidEntry(`Line ${lineNumber} has invalid username "${username}"`);
  }
  if (parts.length === 1) {
    return { empty: false, valid: true, username, startDate: null, endDate: null };
  }
  const rangeResult = parseAbsenceDateRange(parts[1], lineNumber);
  return rangeResult.valid ? { ...rangeResult, username } : rangeResult;
}

/**
 * Pure function: Parses ABSENCE.md content and evaluates scheduled absences against `currentDate` (UTC).
 * Bare "NICK" entries are unconditionally absent; "NICK X-Y" entries are absent when the UTC calendar
 * day of `currentDate` falls within `[X, Y]` inclusive. Malformed lines are reported in `errors`.
 * @param {string} content - Raw string content of ABSENCE.md.
 * @param {Date} [currentDate=new Date()] - Date to evaluate scheduled absences against.
 * @returns {{ absentUsers: string[], errors: string[] }} Currently absent usernames and line validation errors.
 */
function parseAbsenceFile(content, currentDate = new Date()) {
  const today = new Date(
    Date.UTC(
      currentDate.getUTCFullYear(),
      currentDate.getUTCMonth(),
      currentDate.getUTCDate()
    )
  );
  const absentUsers = [];
  const errors = [];
  const lines = (content || '').split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const entry = parseAbsenceLine(lines[i], i + 1);
    if (entry.empty) {
      continue;
    }
    if (!entry.valid) {
      errors.push(entry.reason);
      continue;
    }
    if (!entry.startDate || (today >= entry.startDate && today <= entry.endDate)) {
      absentUsers.push(entry.username);
    }
  }
  return { absentUsers, errors };
}

/**
 * Pure function: Validates the contents of ABSENCE.md, checking that every non-empty
 * non-comment line is either "NICK" or "NICK DD.MM.YYYY-DD.MM.YYYY" with valid
 * calendar dates and start <= end.
 * @param {string} content - Raw ABSENCE.md content.
 * @returns {{ valid: boolean, reason?: string }}
 */
function validateAbsenceContent(content) {
  if (typeof content !== 'string') {
    return { valid: false, reason: 'ABSENCE.md content is missing or not a string' };
  }
  const { errors } = parseAbsenceFile(content);
  return errors.length > 0 ? { valid: false, reason: errors[0] } : { valid: true };
}

module.exports = {
  USERNAME_PATTERN,
  parseDdMmYyyy,
  parseAbsenceDateRange,
  parseAbsenceLine,
  parseAbsenceFile,
  validateAbsenceContent
};
