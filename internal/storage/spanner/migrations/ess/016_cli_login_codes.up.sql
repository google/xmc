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

-- One-time CLI login codes issued by /api/callback and redeemed once by /api/cli/token.
CREATE TABLE CliLoginCodes (
  CodeHash BYTES(32) NOT NULL, -- SHA-256 of the code.
  Email STRING(MAX) NOT NULL,
  Name STRING(MAX),
  CodeChallenge STRING(64) NOT NULL, -- S256 PKCE challenge.
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (CodeHash),
  ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));
