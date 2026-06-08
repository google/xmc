// Copyright 2026 Google LLC

import 'jasmine';
import { HtmlSanitizerBuilder, trySanitizeUrl, windowOpen } from './safevalues_helper';
import { SafeHtml, unwrapHtml } from 'safevalues';

describe('SafeValues Helper', () => {
  describe('HtmlSanitizerBuilder', () => {
    it('should sanitize unsafe HTML', () => {
      const sanitizer = new HtmlSanitizerBuilder()
        .allowClassAttributes()
        .build();

      const unsafeHtml = '<div>Hello <script>alert(1)</script><img src="x" onerror="alert(2)"></div>';
      const sanitized: SafeHtml = sanitizer.sanitize(unsafeHtml);
      const unwrapped = unwrapHtml(sanitized).toString();

      expect(unwrapped).not.toContain('<script>');
      expect(unwrapped).not.toContain('onerror');
      expect(unwrapped).toContain('<div>Hello ');
    });

    it('should preserve class attributes when allowed', () => {
      const sanitizer = new HtmlSanitizerBuilder()
        .allowClassAttributes()
        .build();

      const htmlWithClass = '<div class="test-class">Hello</div>';
      const sanitized = sanitizer.sanitize(htmlWithClass);
      const unwrapped = unwrapHtml(sanitized).toString();

      expect(unwrapped).toContain('class="test-class"');
    });
  });

  describe('trySanitizeUrl', () => {
    it('should allow safe HTTP/HTTPS URLs', () => {
      expect(trySanitizeUrl('https://example.com/test')).toBe('https://example.com/test');
      expect(trySanitizeUrl('http://localhost:8080')).toBe('http://localhost:8080');
      expect(trySanitizeUrl('/relative/path')).toBe('/relative/path');
    });

    it('should sanitize javascript: URLs', () => {
      expect(trySanitizeUrl('javascript:alert(1)')).toBe('about:blank');
      expect(trySanitizeUrl('  JAVASCRIPT:alert(2)  ')).toBe('about:blank');
    });

    it('should sanitize data: URLs', () => {
      expect(trySanitizeUrl('data:text/html,<script>alert(1)</script>')).toBe('about:blank');
      expect(trySanitizeUrl('DATA:image/png;base64,123')).toBe('about:blank');
    });
  });

  describe('windowOpen', () => {
    let mockWindow: any;

    beforeEach(() => {
      mockWindow = {
        open: jasmine.createSpy('open').and.returnValue({} as Window)
      };
    });

    it('should call window.open with parameters', () => {
      const result = windowOpen(mockWindow, 'https://example.com', '_blank', 'resizable=yes');
      expect(mockWindow.open).toHaveBeenCalledWith('https://example.com', '_blank', 'resizable=yes');
      expect(result).toBeDefined();
    });
  });
});
