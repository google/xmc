import {Component, OnInit, ChangeDetectionStrategy} from '@angular/core';

/**
 * Home page for the XMC UI.
 */
@Component({
  selector: 'xmc-home-page',
  standalone: true,
  templateUrl: './home_page.ng.html',
  styleUrl: './home_page.css',
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class HomePage implements OnInit {
  ngOnInit() {}
}
