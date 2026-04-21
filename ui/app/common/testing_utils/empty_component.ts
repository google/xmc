import {ChangeDetectionStrategy, Component} from '@angular/core';

/** Empty component for testing, useful when setting up fake routes. */
@Component({
  standalone: true,
  template: '',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class EmptyComponent {
}
