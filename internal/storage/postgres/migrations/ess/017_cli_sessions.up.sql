-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--      http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- Mirrors spanner/migrations/ess/017_cli_sessions.up.sql. PostgreSQL has no TTL policy and no
-- session store implementation yet.
CREATE TABLE CliSessions (
  Jti TEXT NOT NULL, -- The token's jti claim.
  Email TEXT NOT NULL,
  IssuedAt TIMESTAMPTZ NOT NULL,
  ExpiresAt TIMESTAMPTZ NOT NULL,
  RevokedAt TIMESTAMPTZ, -- NULL while the session is active.
  PRIMARY KEY (Jti)
);
