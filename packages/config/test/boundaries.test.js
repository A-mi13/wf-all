import { fileURLToPath } from 'node:url';
import { ESLint } from 'eslint';
import { describe, expect, test } from 'vitest';
import { boundariesConfig } from '../eslint/boundaries.js';

const root = fileURLToPath(new URL('./fixtures', import.meta.url));
const eslint = new ESLint({
  cwd: root,
  overrideConfigFile: true,
  overrideConfig: [{ files: ['**/*.js'] }, boundariesConfig(root)],
});

const ruleIds = async (file) => {
  const [result] = await eslint.lintFiles([file]);
  return result.messages.map((m) => m.ruleId);
};

describe('границы приложений', () => {
  test('веб не может импортировать админку', async () => {
    expect(await ruleIds('apps/web/src/uses-admin.js')).toContain('boundaries/dependencies');
  });
  test('свой код импортировать можно', async () => {
    expect(await ruleIds('apps/web/src/ok.js')).not.toContain('boundaries/dependencies');
  });
});
