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

-- Mirrors spanner/migrations/ess/016_cli_login_codes.up.sql. PostgreSQL has no TTL policy and no
-- code store implementation yet.
CREATE TABLE CliLoginCodes (
  CodeHash BYTEA NOT NULL, -- SHA-256 of the code.
  Email TEXT NOT NULL,
  Name TEXT,
  CodeChallenge TEXT NOT NULL, -- S256 PKCE challenge.
  ExpiresAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (CodeHash)
);
