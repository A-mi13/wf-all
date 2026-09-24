import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import openapiTS, { astToString } from 'openapi-typescript';
import { parse } from 'yaml';

const load = async (name) =>
  parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));

test('схема Problem одинакова в public и admin', async () => {
  const [pub, adm] = await Promise.all([load('public'), load('admin')]);
  assert.deepEqual(adm.components.schemas.Problem, pub.components.schemas.Problem);
  assert.deepEqual(adm.components.responses.Problem, pub.components.responses.Problem);
});

for (const name of ['public', 'admin']) {
  test(`${name}.yaml генерирует TS-типы`, async () => {
    const out = astToString(await openapiTS(new URL(`../openapi/${name}.yaml`, import.meta.url)));
    assert.match(out, /"\/v1\/health"/);
  });
}
