import tseslint from 'typescript-eslint';
import simpleImportSort from 'eslint-plugin-simple-import-sort';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    files: ['**/*.ts'],
    extends: [
      ...tseslint.configs.recommended,
    ],
    languageOptions: {
      parser: tseslint.parser,
    },
    plugins: {
      'simple-import-sort': simpleImportSort,
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      'simple-import-sort/imports': [
        'error',
        {
          groups: [
            // Side effect imports (e.g. polyfills, CSS/SCSS).
            ['^\\u0000'],
            // Third-party packages (e.g. @angular, rxjs, jasmine).
            ['^@?\\w'],
            // First-party absolute imports (starts with xmanager_cloud/).
            ['^xmanager_cloud/'],
            // Other absolute imports (e.g. stubs or un-grouped libraries).
            ['^'],
            // Relative imports (starts with a dot).
            ['^\\.']
          ]
        }
      ],
      'simple-import-sort/exports': 'error',
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
      'max-lines-per-function': ['warn', { max: 35, skipComments: true }],
      'complexity': ['warn', { max: 10, variant: 'modified' }],
      'max-depth': ['error', 3],
    },
  },
  {
    files: [
      '**/*_test.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/stubs/**',
      '**/mock_*.ts',
    ],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
      'max-lines-per-function': 'off',
      'complexity': 'off',
    },
  },
  {
    files: ['**/*.ts'],
    ignores: [
      '**/*_test.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/stubs/**',
      '**/mock_*.ts',
      '**/app_config.ts',
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: 'Identifier[name=/^(Mock[A-Z]|mock[A-Z]|mock$)/]',
          message: 'Identifiers starting with "Mock" or "mock" are not allowed in production files. Move mock classes and data to dedicated testing/mock files, and only reference them in app_config.ts for DI provisioning.',
        },
      ],
    },
  },
  eslintConfigPrettier
);
