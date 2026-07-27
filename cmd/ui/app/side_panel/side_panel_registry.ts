// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { Type } from '@angular/core';

import { BaseSidePanelContent, BaseSidePanelData } from './base_side_panel';
import {
  EditExperimentForm,
  EditExperimentFormData,
} from './contents/edit_experiment_form/edit_experiment_form';
import { QueueJobsTable, QueueJobsTableData } from './contents/queue_jobs_table/queue_jobs_table';

type SidePanelComponent = BaseSidePanelContent<BaseSidePanelData>;

/**
 * Registry of side panel components.
 *
 * This registry is used to map side panel data types to side panel component
 * types.
 *
 * All side panel types must be registered in this registry.
 */
export const SIDE_PANEL_REGISTRY: Map<Type<BaseSidePanelData>, Type<SidePanelComponent>> = new Map<
  Type<BaseSidePanelData>,
  Type<SidePanelComponent>
>([
  [
    EditExperimentFormData as Type<BaseSidePanelData>,
    EditExperimentForm as Type<SidePanelComponent>,
  ],
  [QueueJobsTableData as Type<BaseSidePanelData>, QueueJobsTable as Type<SidePanelComponent>],
]);
