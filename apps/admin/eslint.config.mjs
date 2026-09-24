import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import { defineConfig, globalIgnores } from 'eslint/config';
import reactHooks from 'eslint-plugin-react-hooks';
import { reactRefresh } from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig([
  globalIgnores(['dist/**', 'src/api/gen/**']),
  tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  reactRefresh.configs.vite(),
  boundariesConfig(repoRoot),
]);
