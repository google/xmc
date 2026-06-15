// Copyright 2025 Google LLC
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
import { InputSignal } from '@angular/core';

/**
 * Data for opening a side panel.
 * All side panel data must extend from this type.
 *
 * This is used to open a side panel.
 */
export type BaseSidePanelData = object;

/**
 * Base side panel content component type.
 * All side panel content components must implement this interface.
 */
export interface BaseSidePanelContent<T extends BaseSidePanelData> {
  data: InputSignal<T>;
}
