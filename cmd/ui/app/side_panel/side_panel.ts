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
import { CommonModule } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  inject,
  OnDestroy,
  OnInit,
  Type,
  ViewChild,
  ViewContainerRef,
} from '@angular/core';
import { MatSidenavModule } from '@angular/material/sidenav';
import { Subscription } from 'rxjs';

import { BaseSidePanelData } from './base_side_panel';
import { SIDE_PANEL_REGISTRY } from './side_panel_registry';
import { SidePanelService } from './side_panel_service';

/**
 * Side panel host component for the XMC UI.
 */
@Component({
  selector: 'xmc-side-panel',
  standalone: true,
  templateUrl: './side_panel.ng.html',
  styleUrl: './side_panel.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, MatSidenavModule],
})
export class SidePanel implements OnInit, OnDestroy {
  private readonly sidePanelService = inject(SidePanelService);

  private readonly subscriptions = new Subscription();

  @ViewChild('sidePanelContents', { read: ViewContainerRef })
  sidePanelContentsContainerRef!: ViewContainerRef;

  protected readonly isPanelOpen$ = this.sidePanelService.isPanelOpen$;
  protected readonly dialogRequest$ = this.sidePanelService.dialogRequest$;

  ngOnInit() {
    this.subscriptions.add(
      this.dialogRequest$.subscribe((request: BaseSidePanelData | undefined) => {
        if (request) {
          this.sidePanelContentsContainerRef.clear();

          const componentType = SIDE_PANEL_REGISTRY.get(
            request.constructor as Type<BaseSidePanelData>,
          );
          if (componentType) {
            const componentRef = this.sidePanelContentsContainerRef.createComponent(componentType);
            componentRef.setInput('data', request);
          }
        }
      }),
    );
  }

  closeSidePanel() {
    this.sidePanelService.closeSidePanel();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
  }
}
