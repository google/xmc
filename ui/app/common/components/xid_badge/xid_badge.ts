import {Clipboard} from '@angular/cdk/clipboard';
import {ChangeDetectionStrategy, Component, inject, Input} from '@angular/core';
import {MatChipsModule} from '@angular/material/chips';
import {MatIconModule} from '@angular/material/icon';
import {MatTooltipModule} from '@angular/material/tooltip';
import {SnackbarService} from 'xmanager_cloud/ui/app/service/snackbar_service';

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
  @Input({required: true}) xid!: string|number;
  constructor(private readonly clipboard: Clipboard) {}

  private readonly snackbarService = inject(SnackbarService);


  copy() {
    if (this.clipboard.copy(String(this.xid))) {
      this.snackbarService.open('Copied XID to clipboard.');
    }
  }
}
