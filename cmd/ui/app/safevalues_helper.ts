// Copyright 2026 Google LLC

import { HtmlSanitizerBuilder as RealHtmlSanitizerBuilder, SafeHtml } from 'safevalues';
import { safeElement, safeWindow } from 'safevalues/dom';

export class HtmlSanitizerBuilder {
  private readonly builder = new RealHtmlSanitizerBuilder();

  allowClassAttributes() {
    this.builder.allowClassAttributes();
    return this;
  }

  build() {
    const sanitizer = this.builder.build();
    return {
      sanitize(html: string): SafeHtml {
        return sanitizer.sanitize(html);
      },
    };
  }
}

export function setElementInnerHtml(element: HTMLElement, safeHtml: SafeHtml) {
  safeElement.setInnerHtml(element, safeHtml);
}

export function trySanitizeUrl(url: string): string {
  const trimmed = url.trim();
  const lower = trimmed.toLowerCase();
  if (
    lower.startsWith('http://') ||
    lower.startsWith('https://') ||
    lower.startsWith('/') ||
    lower.startsWith('./') ||
    lower.startsWith('../')
  ) {
    return trimmed;
  }
  return 'about:blank';
}

export function windowOpen(
  window: Window,
  url: string,
  name?: string,
  specs?: string,
): Window | null {
  return safeWindow.open(window, url, name, specs);
}
