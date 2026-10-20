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

-- One row per CLI session token issued by /api/cli/token, keyed by its jti.
CREATE TABLE CliSessions (
  -- The token's jti claim.
  Jti STRING(64) NOT NULL,
  -- Normalized email the token was issued to.
  Email STRING(MAX) NOT NULL,
  -- The token's iat claim.
  IssuedAt TIMESTAMP NOT NULL,
  -- The token's exp claim. Rows are pruned by the TTL policy once the token has expired.
  ExpiresAt TIMESTAMP NOT NULL,
  -- When the session was revoked. NULL while the session is active.
  RevokedAt TIMESTAMP,
) PRIMARY KEY (Jti),
  ROW DELETION POLICY (OLDER_THAN(ExpiresAt, INTERVAL 1 DAY));
