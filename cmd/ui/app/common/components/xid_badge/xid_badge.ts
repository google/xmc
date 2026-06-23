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
import { Clipboard } from '@angular/cdk/clipboard';
import { ChangeDetectionStrategy, Component, inject, input } from '@angular/core';
import { MatChipsModule } from '@angular/material/chips';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';

import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

/**
 * Display a XID badge.
 */
@Component({
  selector: 'xmc-xid-badge',
  standalone: true,
  templateUrl: './xid_badge.ng.html',
  styleUrl: './xid_badge.css',
  imports: [MatChipsModule, MatIconModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class XidBadge {
  readonly xid = input.required<string | number>();
  private readonly clipboard = inject(Clipboard);
  private readonly snackbarService = inject(SnackbarService);

  copy(): void {
    if (this.clipboard.copy(String(this.xid()))) {
      this.snackbarService.open('Copied XID to clipboard.');
    }
  }
}
