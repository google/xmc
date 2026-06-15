import angular from '@angular-eslint/eslint-plugin';
import tseslint from 'typescript-eslint';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import importPlugin from 'eslint-plugin-import';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tseslint.parser,
    },
    plugins: {
      '@angular-eslint': angular,
      'simple-import-sort': simpleImportSort,
      'import': importPlugin,
    },
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/../../**'],
              message: 'Relative imports must not go up more than one level. Use absolute paths (xmanager_cloud/...) instead.',
            },
          ],
        },
      ],
    },
  },
  eslintConfigPrettier
);
