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

-- Ownership fence: the single cluster allowed to write Component intents, at its epoch.
-- CMS writes only FenceId = 1. There is no singleton CHECK: FenceId is reserved for partitioned
-- fences.
CREATE TABLE ComponentOwnershipFence (
  FenceId INT64 NOT NULL,
  ActiveClusterUid STRING(64) NOT NULL,
  ActiveClusterName STRING(63) NOT NULL,
  Epoch INT64 NOT NULL,
  DeclaredTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  LastSeenTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  CONSTRAINT CK_ComponentOwnershipFence_Epoch CHECK (Epoch >= 1),
) PRIMARY KEY (FenceId);
