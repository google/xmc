// Copyright 2026 Google LLC
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

import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

import { HealthStatusResponse } from 'xmanager_cloud/health_service/proto/api.jsonpb_decls';

type SystemInfo = NonNullable<HealthStatusResponse['systemInfo']>;

/** A component showing system version info and external monitoring links. */
@Component({
  selector: 'xmc-system-info-card',
  standalone: true,
  imports: [MatIconModule],
  templateUrl: './system_info_card.ng.html',
  styleUrl: './system_info_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SystemInfoCardComponent {
  systemInfo = input.required<SystemInfo>();
}
