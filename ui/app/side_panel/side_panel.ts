import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, inject, OnDestroy, OnInit, Type, ViewChild, ViewContainerRef} from '@angular/core';
import {MatSidenavModule} from '@angular/material/sidenav';
import {Subscription} from 'rxjs';

import {BaseSidePanelData} from './base_side_panel';
import {SIDE_PANEL_REGISTRY} from './side_panel_registry';
import {SidePanelService} from './side_panel_service';

/**
 * Side panel host component for the XMC UI.
 */
@Component({
  selector: 'xmc-side-panel',
  standalone: true,
  templateUrl: './side_panel.ng.html',
  styleUrl: './side_panel.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatSidenavModule,
  ],
})
export class SidePanel implements OnInit, OnDestroy {
  private readonly sidePanelService = inject(SidePanelService);

  private readonly subscriptions = new Subscription();

  @ViewChild('sidePanelContents', {read: ViewContainerRef})
  sidePanelContentsContainerRef!: ViewContainerRef;

  protected readonly isPanelOpen$ = this.sidePanelService.isPanelOpen$;
  protected readonly dialogRequest$ = this.sidePanelService.dialogRequest$;

  ngOnInit() {
    this.subscriptions.add(this.dialogRequest$.subscribe(
        (request: BaseSidePanelData|undefined) => {
          if (request) {
            this.sidePanelContentsContainerRef.clear();

            const componentType = SIDE_PANEL_REGISTRY.get(
                request.constructor as Type<BaseSidePanelData>);
            if (componentType) {
              const componentRef =
                  this.sidePanelContentsContainerRef.createComponent(
                      componentType);
              componentRef.setInput('data', request);
            }
          }
        }));
  }

  closeSidePanel() {
    this.sidePanelService.closeSidePanel();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
  }
}
