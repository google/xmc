import {InputSignal} from '@angular/core';

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
