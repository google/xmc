// Copyright 2025 Google LLC
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
import './mock_document';

import {renderMermaid, THEMES} from 'beautiful_mermaid';
import * as fs from 'fs';

async function main() {
  const args = process.argv.slice(2);
  if (args.length < 2) {
    console.error('Usage: node render_mermaid.js <input.mmd> <output.svg>');
    process.exit(1);
  }

  const inputFile = args[0];
  const outputFile = args[1];

  try {
    const inputContent = fs.readFileSync(inputFile, 'utf-8');
    const svg = await renderMermaid(inputContent, {
      font: 'Arial',
      ...THEMES['github-light'],
    });
    fs.writeFileSync(outputFile, svg);
  } catch (error) {
    console.error('Error rendering mermaid:', error);
    process.exit(1);
  }
}

main();
