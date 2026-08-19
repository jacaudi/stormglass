import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
  },
  {
    // The layout probe: Node ESM, not browser TS. Without this block eslint's
    // flat config matches these files with no rules at all and reports success
    // on code it never read.
    files: ['test/layout/**/*.mjs'],
    extends: [js.configs.recommended],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      // BOTH: these are Node modules, but probe.mjs's body is the function
      // Playwright serialises into the page, so it references document, window,
      // getComputedStyle and SVGElement. With globals.node alone eslint reports
      // 19 no-undef errors and `task ci` fails.
      globals: { ...globals.node, ...globals.browser },
    },
  },
])
