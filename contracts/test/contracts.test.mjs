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

// Путь-образец каждого контракта есть в сгенерированных типах.
const samplePath = { public: '/v1/cities/nearest', admin: '/v1/staff/me' };

for (const name of ['public', 'admin']) {
  test(`${name}.yaml генерирует TS-типы`, async () => {
    const out = astToString(await openapiTS(new URL(`../openapi/${name}.yaml`, import.meta.url)));
    assert.ok(out.includes(`"${samplePath[name]}"`), `нет пути ${samplePath[name]}`);
  });
}

// Спека media §7.1: общие схемы файлов одинаковы в public и admin (операции владельцев в обоих контрактах); у admin —
// ещё multipart прямой загрузки. UploadTicket.method и MediaImage.state — открытые enum контракта.
test('схемы media одинаковы в public и admin', async () => {
  const [pub, adm] = await Promise.all([load('public'), load('admin')]);
  for (const name of ['UploadRequest', 'UploadTicket', 'MediaImage', 'PrivateMediaLink']) {
    assert.ok(pub.components.schemas[name], `public: нет ${name}`);
    assert.deepEqual(adm.components.schemas[name], pub.components.schemas[name], name);
  }
  const s = pub.components.schemas;
  assert.deepEqual(s.UploadTicket.properties.method.enum, ['PUT', 'POST']);
  assert.equal(s.UploadTicket.properties.method['x-extensible-enum'], true);
  assert.deepEqual(s.MediaImage.properties.state.enum, ['processing', 'ready', 'rejected']);
  assert.equal(s.MediaImage.properties.state['x-extensible-enum'], true);
  const direct = adm.components.schemas.MediaDirectUpload;
  assert.equal(direct.properties.file.format, 'binary');
  assert.equal(pub.components.schemas.MediaDirectUpload, undefined);
});
