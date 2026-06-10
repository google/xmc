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
ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.ArtifactPayload,
  cloud.xmanager.v1.ManagedXprofArtifact,
  cloud.xmanager.v1.XProfUIInstance,
  cloud.xmanager.v1.CodeSourceArtifact,
  cloud.xmanager.v1.TextArtifact
);

ALTER TABLE Artifacts ADD COLUMN Payload cloud.xmanager.v1.ArtifactPayload;
ALTER TABLE Artifacts ADD COLUMN SearchableContent STRING(MAX);
ALTER TABLE Artifacts ADD COLUMN SearchableContent_Tokens TOKENLIST AS
(TOKENIZE_FULLTEXT(SearchableContent)) HIDDEN;

ALTER SEARCH INDEX ArtifactsSearchIndex ADD COLUMN SearchableContent_Tokens;

