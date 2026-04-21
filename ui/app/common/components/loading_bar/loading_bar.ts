import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatProgressBarModule} from '@angular/material/progress-bar';

/**
 * Display a loading bar.
 */
@Component({
  selector: 'xmc-loading-bar',
  standalone: true,
  templateUrl: './loading_bar.ng.html',
  styleUrl: './loading_bar.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatProgressBarModule],
})
export class LoadingBar {
  readonly isRefreshing = input<boolean>(false);
}
