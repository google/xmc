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

-- Recorded owner of a component: the declarative object (Kubernetes CR) that last wrote it.
-- All columns are NULL for records never written by a sourced request.
ALTER TABLE Components ADD COLUMN SourceUid STRING(64);
ALTER TABLE Components ADD COLUMN SourceGeneration INT64;
ALTER TABLE Components ADD COLUMN SourceNamespace STRING(63);
ALTER TABLE Components ADD COLUMN SourceClusterUid STRING(64);
ALTER TABLE Components ADD COLUMN SourceEpoch INT64;
ALTER TABLE Components ADD COLUMN SourceLastSeenTime TIMESTAMP OPTIONS (allow_commit_timestamp = true);
