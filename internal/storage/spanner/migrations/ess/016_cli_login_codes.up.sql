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
  -- SHA-256 of the code. The code itself is never stored.
  CodeHash BYTES(32) NOT NULL,
  -- Normalized email of the admitted user.
  Email STRING(MAX) NOT NULL,
  -- Display name returned by the identity provider.
  Name STRING(MAX),
  -- S256 PKCE challenge the redeeming verifier must match.
  CodeChallenge STRING(64) NOT NULL,
  -- The code is rejected after this time. Expired rows are pruned by the TTL policy.
  ExpiresAt TIMESTAMP NOT NULL,
) PRIMARY KEY (CodeHash),
  ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));
