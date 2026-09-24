// Страж подключения: корневой eslint.config.mjs действительно проверяет границы в packages/**.
// Политику проверяют фикстуры (boundaries.test.js), здесь — что её применяют к настоящим пакетам.
import { fileURLToPath } from 'node:url';
import { ESLint } from 'eslint';
import { describe, expect, test } from 'vitest';

const repoRoot = fileURLToPath(new URL('../../..', import.meta.url));
// cwd = корень репозитория: ESLint берёт корневой eslint.config.mjs, как `pnpm run lint:packages`
const eslint = new ESLint({ cwd: repoRoot });

const ruleIds = async (code, file) => {
  const [result] = await eslint.lintText(code, { filePath: `${repoRoot}/${file}` });
  return result.messages.map((m) => m.ruleId);
};

describe('линт пакетов в репозитории', () => {
  test('пакет с импортом из веба не проходит', async () => {
    const code = "import web from '../../../apps/web/package.json';\nexport default web;\n";
    expect(await ruleIds(code, 'packages/i18n/src/planted.ts')).toContain(
      'boundaries/dependencies',
    );
  });
  test('пакет с импортом из админки не проходит', async () => {
    const code = "import admin from '../../../apps/admin/package.json';\nexport default admin;\n";
    expect(await ruleIds(code, 'packages/tokens/scripts/planted.mjs')).toContain(
      'boundaries/dependencies',
    );
  });
  test('импорт внутри пакета проходит', async () => {
    const code = "import { withFallback } from './index';\nexport default withFallback;\n";
    expect(await ruleIds(code, 'packages/i18n/src/planted.ts')).toEqual([]);
  });
});
