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

import { Pipe, PipeTransform } from '@angular/core';
import { DomSanitizer, SafeUrl } from '@angular/platform-browser';

import { trySanitizeUrl } from 'xmanager_cloud/ui/app/safevalues_helper';

/**
 * A pipe that sanitizes a URL and wraps it in a SafeUrl to bypass Angular's Built-in HTML sanitization.
 */
@Pipe({
  name: 'safeUrl',
  standalone: true,
})
export class SafeUrlPipe implements PipeTransform {
  constructor(private readonly sanitizer: DomSanitizer) {}

  transform(value: string | null | undefined): SafeUrl {
    if (!value) {
      return '';
    }
    const sanitized = trySanitizeUrl(value);
    return this.sanitizer.bypassSecurityTrustUrl(sanitized);
  }
}
