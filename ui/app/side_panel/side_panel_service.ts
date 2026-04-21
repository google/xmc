import {Injectable} from '@angular/core';
import {BehaviorSubject, Observable} from 'rxjs';
import {map} from 'rxjs/operators';

import {BaseSidePanelData} from './base_side_panel';

/** Service for the side panel. */
@Injectable({providedIn: 'root'})
export class SidePanelService {
  private readonly data$ =
      new BehaviorSubject<BaseSidePanelData|undefined>(undefined);

  /** Emits the dialog request if it exists, otherwise undefined. */
  readonly dialogRequest$: Observable<BaseSidePanelData|undefined> =
      this.data$.asObservable();

  /** Emits true if the side panel is open, else false. */
  readonly isPanelOpen$: Observable<boolean> = this.data$.pipe(
      map((data) => data !== undefined),
  );

  /** Opens the side panel as the top most dialog with the given request. */
  openSidePanel(request: BaseSidePanelData) {
    this.data$.next(request);
  }

  /** Closes the top most dialog from the side panel. */
  closeSidePanel() {
    this.data$.next(undefined);
  }
}
