import {Type} from '@angular/core';

import {BaseSidePanelContent, BaseSidePanelData} from './base_side_panel';
import {EditExperimentForm, EditExperimentFormData} from './contents/edit_experiment_form/edit_experiment_form';

type SidePanelComponent = BaseSidePanelContent<BaseSidePanelData>;

/**
 * Registry of side panel components.
 *
 * This registry is used to map side panel data types to side panel component
 * types.
 *
 * All side panel types must be registered in this registry.
 */
export const SIDE_PANEL_REGISTRY:
    Map<Type<BaseSidePanelData>, Type<SidePanelComponent>> = new Map([
      [
        EditExperimentFormData,
        EditExperimentForm as Type<SidePanelComponent>,
      ],
    ]);
