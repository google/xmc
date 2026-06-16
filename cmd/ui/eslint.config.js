import tseslint from 'typescript-eslint';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tseslint.parser,
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
