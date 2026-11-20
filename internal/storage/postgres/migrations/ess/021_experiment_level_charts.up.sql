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

-- Experiment-level charts (for example the parallel-coordinates chart of a study) belong to no
-- work unit, so they store a NULL Wid. Work-unit charts keep storing their wid.
ALTER TABLE Charts ALTER COLUMN Wid DROP NOT NULL, ALTER COLUMN Wid DROP DEFAULT;
