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

-- WorkUnit.parameters, a proto-marshaled google.protobuf.Struct. It is stored as BYTES rather than
-- as a proto column so that google.protobuf.Struct stays out of the proto bundle. The bundled
-- cloud.xmanager.v1.WorkUnit descriptor, which backs the WorkUnitType enum column and the
-- Operations.Response column, is not refreshed: WorkUnit values written to Operations.Response with
-- fields 11 (parameters) and 12 (trial_id) keep them as unknown fields, which read back unchanged.
-- NULL for work units that are not study trials, including all rows written before this column
-- existed.
ALTER TABLE WorkUnits ADD COLUMN Parameters BYTES(MAX);

-- WorkUnit.trial_id, the optimization engine's trial ID. NULL for work units that are not study
-- trials.
ALTER TABLE WorkUnits ADD COLUMN TrialId STRING(MAX);
