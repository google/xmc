-- Copyright 2025 Google LLC
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

-- NOTE: This data migration script must be executed manually to migrate existing data.

UPDATE Artifacts
SET Payload = CASE
  WHEN DataType = 'CODE_SOURCE' THEN NEW cloud.xmanager.v1.ArtifactPayload(
    NEW cloud.xmanager.v1.CodeSourceArtifact(
      AdditionalInfo.title AS title,
      AdditionalInfo.code_block AS code_block,
      Url AS code_url
    ) AS code_source
  )
  WHEN DataType = 'TEXT' THEN NEW cloud.xmanager.v1.ArtifactPayload(
    NEW cloud.xmanager.v1.TextArtifact(
      AdditionalInfo.title AS title,
      AdditionalInfo.text AS text
    ) AS text
  )
  ELSE Payload
END
WHERE DataType IN ('CODE_SOURCE', 'TEXT') AND AdditionalInfo IS NOT NULL;
