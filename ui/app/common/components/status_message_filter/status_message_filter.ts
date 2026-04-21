import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {MatButtonModule} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatCheckboxModule} from '@angular/material/checkbox';
import {MatIconModule} from '@angular/material/icon';
import {MatMenuModule} from '@angular/material/menu';
import {MatTooltipModule} from '@angular/material/tooltip';
import {StructuredMessage} from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {ALL_SEVERITIES, ALL_SOURCES, formatSource, StatusMessageFilterService} from 'xmanager_cloud/ui/app/service/status_message_filter_service';

/**
 * Filter component for status messages.
 */
@Component({
  selector: 'xmc-status-message-filter',
  standalone: true,
  templateUrl: './status_message_filter.ng.html',
  styleUrl: './status_message_filter.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatTooltipModule,
    MatCardModule,
    MatCheckboxModule,
  ],
})
export class StatusMessageFilter {
  protected readonly filterService = inject(StatusMessageFilterService);
  protected readonly formatSource = formatSource;

  protected readonly allSeverities = ALL_SEVERITIES;
  protected readonly allSources = ALL_SOURCES;

  toggleSeverity(severity: StructuredMessage.Severity, isChecked: boolean) {
    const current = new Set(this.filterService.severitiesSubject.value);
    isChecked ? current.add(severity) : current.delete(severity);
    this.filterService.updateSeverities(current);
  }

  toggleSource(source: StructuredMessage.Source, isChecked: boolean) {
    const current = new Set(this.filterService.sourcesSubject.value);
    isChecked ? current.add(source) : current.delete(source);
    this.filterService.updateSources(current);
  }

  protected formatSeverity(severity: StructuredMessage.Severity): string {
    return severity.charAt(0) + severity.slice(1).toLowerCase();
  }
}
