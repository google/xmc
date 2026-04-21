import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatChipsModule} from '@angular/material/chips';
import {Priority} from 'xmanager_cloud/experiment_state_server/proto/priority.jsonpb_decls';

/**
 * Component for displaying priority as a colored chip.
 */
@Component({
  selector: 'xmc-priority-chip',
  standalone: true,
  styleUrl: './priority_chip.scss',
  templateUrl: './priority_chip.ng.html',

  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PriorityChip {
  readonly priority = input.required<Priority|undefined>();
}
