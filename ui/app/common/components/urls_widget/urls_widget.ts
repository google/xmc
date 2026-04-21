import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatButtonModule} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatIconModule} from '@angular/material/icon';
import {MatTooltipModule} from '@angular/material/tooltip';
import {ProcessedUrl} from 'xmanager_cloud/ui/app/common/url/url_processor';

/**
 * Component for displaying URLs.
 */
@Component({
  selector: 'xmc-urls-widget',
  standalone: true,
  styleUrl: './urls_widget.scss',
  templateUrl: './urls_widget.ng.html',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatIconModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class UrlsWidget {
  readonly urls = input.required<ProcessedUrl[]>();
}
