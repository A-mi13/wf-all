// Публичные операции geo и min-version (спека geo §4): пути, теги, анонимность, коды, лимит,
// язык и кэш-заголовки — на собранном бандле.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { operations } from '../src/conventions.mjs';

const doc = parse(await readFile(new URL('../openapi/public.yaml', import.meta.url), 'utf8'));
const ops = new Map(operations(doc).map((o) => [o.op.operationId, o]));
const LANG = '#/components/parameters/AcceptLanguage';
// заголовок ответа: свой объект или $ref на components.headers
const header = (h) => (h?.$ref ? doc.components.headers[h.$ref.split('/').at(-1)] : h);
const params = (id) =>
  Object.fromEntries(
    (ops.get(id).op.parameters ?? []).filter((p) => p.name).map((p) => [p.name, p]),
  );

const CATALOG = 'public, max-age=300';
const expected = {
  listCities: { path: '/v1/cities', tag: 'geo', codes: [], cache: CATALOG },
  getCity: {
    path: '/v1/cities/{cityId}',
    tag: 'geo',
    codes: ['geo.city_not_found'],
    cache: CATALOG,
  },
  getCityBySlug: {
    path: '/v1/cities/by-slug/{slug}',
    tag: 'geo',
    codes: ['geo.city_not_found'],
    cache: CATALOG,
  },
  findNearestCity: {
    path: '/v1/cities/nearest',
    tag: 'geo',
    codes: [],
    cache: 'private, no-store',
    rateLimit: 'geo_nearest',
  },
  getAppMinVersion: {
    path: '/v1/app/min-version',
    tag: 'platform',
    codes: [],
    cache: 'public, max-age=60',
  },
};

for (const [id, want] of Object.entries(expected)) {
  test(`${id}: ${want.path}, тег ${want.tag}, анонимная, коды и класс лимита`, () => {
    const o = ops.get(id);
    assert.ok(o, `нет операции ${id}`);
    assert.equal(o.path, want.path);
    assert.equal(o.verb, 'get');
    assert.deepEqual(o.op.tags, [want.tag]);
    assert.deepEqual(o.op.security, []);
    assert.deepEqual(o.op['x-error-codes'], want.codes);
    assert.equal(o.op['x-rate-limit'], want.rateLimit);
  });

  test(`${id}: ответ 200 — Cache-Control «${want.cache}», язык только у geo`, () => {
    const op = ops.get(id).op;
    const headers = op.responses['200'].headers ?? {};
    const cache = header(headers['Cache-Control']);
    assert.equal(cache?.required, true);
    assert.equal(cache?.example, want.cache);
    const localized = want.tag === 'geo';
    assert.equal(header(headers.Vary)?.required === true, localized);
    const lang = header(headers['Content-Language']);
    assert.equal(Boolean(lang) && lang.required !== true, localized);
    assert.equal(
      (op.parameters ?? []).some((p) => p.$ref === LANG),
      localized,
    );
  });
}

test('/v1/health удалён из публичного контракта (спека geo §4.4)', () => {
  assert.equal(doc.paths['/v1/health'], undefined);
  assert.equal(doc.components.schemas.Health, undefined);
});

test('nearest: lat и lon обязательны, number/double в границах', () => {
  const p = params('findNearestCity');
  for (const [name, bound] of [
    ['lat', 90],
    ['lon', 180],
  ]) {
    assert.equal(p[name]?.in, 'query');
    assert.equal(p[name].required, true);
    const s = p[name].schema;
    assert.deepEqual([s.type, s.format, s.minimum, s.maximum], ['number', 'double', -bound, bound]);
  }
});

test('listCities: q 1–100 символов, limit 1–100 (20), страница items и next_cursor', () => {
  const p = params('listCities');
  assert.deepEqual([p.q.schema.minLength, p.q.schema.maxLength], [1, 100]);
  const l = p.limit.schema;
  assert.deepEqual([l.minimum, l.maximum, l.default], [1, 100, 20]);
  const page = doc.components.schemas.CityPage;
  assert.deepEqual(page.required, ['items', 'next_cursor']);
  assert.equal(page.properties.next_cursor.nullable, true);
});

test('CityStatus и AppPlatform — открытые enum', () => {
  const s = doc.components.schemas;
  assert.deepEqual(s.CityStatus.enum, ['waitlist', 'pilot', 'live']);
  assert.deepEqual(s.AppPlatform.enum, ['ios', 'android']);
  assert.equal(s.CityStatus['x-extensible-enum'], true);
  assert.equal(s.AppPlatform['x-extensible-enum'], true);
});
