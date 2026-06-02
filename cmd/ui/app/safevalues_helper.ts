// Copyright 2026 Google LLC
export class HtmlSanitizerBuilder {
  allowClassAttributes() {
    return this;
  }
  build() {
    return {
      sanitize(html: string): string {
        return html;
      }
    };
  }
}

export function setElementInnerHtml(element: HTMLElement, safeHtml: string) {
  element.innerHTML = safeHtml;
}

export function trySanitizeUrl(url: string): string {
  const lower = url.trim().toLowerCase();
  if (lower.startsWith('javascript:') || lower.startsWith('data:')) {
    return 'about:blank';
  }
  return url;
}

export function windowOpen(window: Window, url: string, name?: string, specs?: string): Window | null {
  return window.open(url, name, specs);
}
