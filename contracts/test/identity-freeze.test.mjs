// Страж заморозки контракта identity (план I-1, R17). oasdiff видит только ломающее, а x-error-codes,
// x-anonymous-mutations, x-rate-limit, x-idempotency и описания не видит вовсе; этот тест хэширует срез identity
// public и 11 операций admin и падает на любой его правке. Граница среза — contracts/src/freeze.mjs.
//
// Правка среза — только через контроллера плана: запись в «Журнал изменений после заморозки» документа передачи
// нативщику (.superpowers/sdd/2026-11-13-identity-1/native-handoff.md), новый хэш — в константе ниже. FREEZE_OWN
// правится только вместе с хэшем.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { bullet, operationsOf, section, sha256, slice } from '../src/freeze.mjs';

const PUBLIC_IDENTITY_SHA256 = '3fe07ab95f250fc31559bc9b43604d24df78d34f5c203530828429f684f3edf7';
const ADMIN_IDENTITY_SHA256 = '0b5784d39fcb3500779cef2a75def42dc4f93d68c4616039e776cd1b2598afa4';

const load = async (path) =>
  parse(await readFile(new URL(`../openapi/${path}`, import.meta.url), 'utf8'));

// Поля Problem и описание Retry-After, которые ввёл I-1 (R3, §6.6); остальное в Problem — общее платформы.
const PROBLEM_OWN = [
  'schemas/Problem/properties/reasons',
  'schemas/Problem/properties/methods',
  'schemas/Problem/properties/available_at',
  'schemas/Problem/properties/blocked_until',
  'responses/Problem/headers/Retry-After/description',
];

// FREEZE_OWN — то, что I-1 ввёл вне identity/schemas.yaml: в корнях и чужих файлах.
const FREEZE_OWN = {
  public: [
    ...PROBLEM_OWN,
    'parameters/CodeId',
    'parameters/SessionId',
    'parameters/IdentityId',
    'parameters/PasskeyId',
    'parameters/ApplicationId',
    'parameters/Cursor',
    'parameters/Limit',
    'headers/CacheNoStore',
    'headers/CachePrivate',
  ],
  admin: [
    ...PROBLEM_OWN,
    'schemas/StaffPlayerLink',
    'schemas/StaffPlayerLinkPut',
    'schemas/StaffAccount/properties/player_link',
    'parameters/LookupEmail',
    'parameters/AccessReason',
    'parameters/ApplicationId',
    'parameters/UserId',
    'parameters/ResetId',
  ],
};

const STAFF_LINK_OPS = ['putStaffPlayerLink', 'deleteStaffPlayerLink'];
const isIdentity = (op) => op.tags?.includes('identity');

const [pub, adm, pubOwn, admOwn] = await Promise.all([
  load('public.yaml'),
  load('admin.yaml'),
  load('public/identity/schemas.yaml'),
  load('admin/identity/schemas.yaml'),
]);

const ownOf = (schemas, extra) => [...Object.keys(schemas).map((n) => `schemas/${n}`), ...extra];

const LOGIN_SECTION = '**Вход и токены.**';
const RESTRICTED = '- `identity.restricted`';

function publicSlice(doc) {
  return slice(doc, {
    keep: isIdentity,
    own: ownOf(pubOwn, FREEZE_OWN.public),
    extra: {
      'x-anonymous-mutations': doc['x-anonymous-mutations'],
      'x-error-codes-common': doc['x-error-codes-common'],
      info: {
        login: section(doc.info.description, LOGIN_SECTION),
        restricted: bullet(doc.info.description, RESTRICTED),
      },
    },
  });
}

function adminSlice(doc) {
  return slice(doc, {
    keep: (op) => isIdentity(op) || STAFF_LINK_OPS.includes(op.operationId),
    own: ownOf(admOwn, FREEZE_OWN.admin),
  });
}

const failure = (name, got) =>
  `правка замороженного контракта identity (${name}): только через контроллера, запись в «Журнал изменений после ` +
  'заморозки» native-handoff.md, новый хэш в этом тесте. Если менялись Problem или x-error-codes-common — это ' +
  `общая часть public, затрагивает нативщика, запись в журнал обязательна. Новый хэш: ${got}`;

test('identity-freeze: срез public совпадает с замороженным', () => {
  const got = sha256(publicSlice(pub));
  assert.equal(got, PUBLIC_IDENTITY_SHA256, failure('public', got));
});

test('identity-freeze: срез admin совпадает с замороженным', () => {
  const got = sha256(adminSlice(adm));
  assert.equal(got, ADMIN_IDENTITY_SHA256, failure('admin', got));
});

test('identity-freeze: срез не вакуумен', () => {
  const p = publicSlice(pub);
  const a = adminSlice(adm);
  assert.equal(p.operations.length, 50);
  assert.equal(a.operations.length, 11);
  // частично свои схемы — обязательность своих полей в срезе (ревью Task 5, I1); объект пути — у каждой операции
  assert.deepEqual(Object.keys(p.required), ['schemas/Problem']);
  assert.deepEqual(Object.keys(a.required).sort(), ['schemas/Problem', 'schemas/StaffAccount']);
  assert.ok([...p.operations, ...a.operations].every((o) => o.item && typeof o.item === 'object'));
  assert.equal(operationsOf(adm, (op) => STAFF_LINK_OPS.includes(op.operationId)).length, 2);
  // каждое имя FREEZE_OWN и каждая своя схема есть в бандле
  for (const [name, s] of [
    ['public', p],
    ['admin', a],
  ]) {
    const missing = Object.entries(s.components)
      .filter(([, v]) => v === null)
      .map(([k]) => k);
    assert.deepEqual(missing, [], `${name}: компонентов среза нет в бандле`);
  }
  assert.ok(p['x-anonymous-mutations'].length >= 16);
  assert.ok(p['x-error-codes-common'].length > 0);
  assert.match(p.info.login, /refreshSession/);
  assert.match(p.info.login, /device_required/);
  assert.doesNotMatch(p.info.login, /Деньги/, 'раздел «Вход и токены» не захватывает общие пункты');
  assert.match(p.info.restricted, /identity\.restricted/);
  assert.doesNotMatch(p.info.restricted, /Вход и токены/);
  // чужое — только строкой $ref: компонента целиком в срезе нет, ссылка на него в операциях есть
  for (const foreign of [
    'ApprovalAccepted',
    'StaffAccount',
    'Approval',
    'ApprovalAction',
    'FeatureFlagKey',
  ]) {
    assert.ok(!(`schemas/${foreign}` in a.components), foreign);
  }
  assert.ok(!('schemas/AppPlatform' in p.components));
  assert.match(JSON.stringify(a.operations), /#\/components\/schemas\/ApprovalAccepted/);
  assert.match(JSON.stringify(a.operations), /#\/components\/parameters\/IdempotencyKey/);
  assert.match(JSON.stringify(p.components), /#\/components\/schemas\/AppPlatform/);
  for (const shared of [
    'parameters/IdempotencyKey',
    'parameters/AdminHeader',
    'parameters/Cursor',
  ]) {
    assert.ok(!(shared in a.components), `admin: ${shared} — общий, не свой`);
  }
});
