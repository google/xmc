// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * XMC Color Palette for Multi-Series and Baseline Curves.
 */
export const XMC_SERIES_PALETTE = [
  '#1a73e8', // Run 1: Google Blue
  '#1e8e3e', // Run 2: Google Green
  '#f9ab00', // Run 3: Google Amber
  '#9334e6', // Run 4: Purple
  '#12b5cb', // Run 5: Cyan
  '#e37400', // Run 6: Deep Orange
  '#d93025', // Run 7: Red
];

export const XMC_BASELINE_COLOR = '#e52592'; // Baseline: Magenta / Deep Pink

export const XMC_SERIES_SYMBOLS: Array<'circle' | 'rect' | 'diamond' | 'triangle' | 'roundRect'> = [
  'circle',
  'rect',
  'diamond',
  'triangle',
  'roundRect',
];

export interface XmcChartThemeConfig {
  fontFamily?: string;
  gridLineColor?: string;
  axisTextColor?: string;
  backgroundColor?: string;
  tooltipBgColor?: string;
  tooltipTextColor?: string;
}

export const DEFAULT_XMC_THEME_CONFIG: XmcChartThemeConfig = {
  fontFamily: "'Google Sans', Roboto, sans-serif",
  gridLineColor: '#f1f3f4',
  axisTextColor: '#5f6368',
  backgroundColor: 'transparent',
  tooltipBgColor: '#202124',
  tooltipTextColor: '#ffffff',
};
