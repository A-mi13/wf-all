import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { buildBundle } from '../scripts/redocly.mjs';

const load = async (path) =>
  parse(await readFile(new URL(`../openapi/${path}`, import.meta.url), 'utf8'));

for (const name of ['public', 'admin']) {
  test(`${name}.yaml — бандл, собранный из ${name}/root.yaml`, async () => {
    const [bundle, root] = await Promise.all([load(`${name}.yaml`), load(`${name}/root.yaml`)]);
    assert.equal(bundle.info.title, root.info.title);
    assert.deepEqual(Object.keys(bundle.paths).sort(), Object.keys(root.paths).sort());
    assert.deepEqual(bundle.paths['/v1/health'].get.tags, ['platform']);
    const refs = JSON.stringify(bundle).match(/"\$ref":"[^"]+"/g) ?? [];
    assert.deepEqual(
      refs.filter((r) => !r.startsWith('"$ref":"#/')),
      [],
      'в бандле остались внешние $ref',
    );
  });
}

// Бандл свежий: сборка исходников redocly совпадает с закоммиченным бандлом —
// устаревший бандл ловится локально, без Go и CI.
for (const name of ['public', 'admin']) {
  test(`${name}.yaml совпадает со свежей сборкой ${name}/root.yaml`, async () => {
    assert.deepEqual(
      parse(buildBundle(name)),
      await load(`${name}.yaml`),
      'бандл устарел: pnpm --filter @wf/contracts bundle',
    );
  });
}
