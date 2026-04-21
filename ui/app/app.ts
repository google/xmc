import {ChangeDetectionStrategy, Component, signal} from '@angular/core';
import {MatIconModule} from '@angular/material/icon';
import {MatListModule} from '@angular/material/list';
import {MatSidenavModule} from '@angular/material/sidenav';
import {RouterModule, RouterOutlet} from '@angular/router';

import {Header} from './common/components/header/header';
import {SidePanel} from './side_panel/side_panel';

/**
 * The root component.
 */
@Component({
  selector: 'app-root',
  templateUrl: './app.ng.html',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './app.css',
  imports: [
    RouterOutlet, Header, SidePanel, MatSidenavModule, MatListModule,
    RouterModule, MatIconModule
  ],
})
export class App {
  title = 'xmc';
  readonly isSidenavOpened = signal(true);

  toggleSidenav() {
    this.isSidenavOpened.set(!this.isSidenavOpened());
  }
}
