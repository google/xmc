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

-- Mirrors spanner/migrations/ess/015_component_ownership_fence.up.sql.
-- Ownership fence: the single cluster allowed to write Component intents, at its epoch.
-- CMS writes only FenceId = 1. There is no singleton CHECK: FenceId is reserved for partitioned
-- fences.
CREATE TABLE ComponentOwnershipFence (
  FenceId BIGINT NOT NULL,
  ActiveClusterUid TEXT NOT NULL,
  ActiveClusterName TEXT NOT NULL,
  Epoch BIGINT NOT NULL,
  DeclaredTime TIMESTAMPTZ NOT NULL,
  LastSeenTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (FenceId),
  CONSTRAINT CK_ComponentOwnershipFence_Epoch CHECK (Epoch >= 1)
);
