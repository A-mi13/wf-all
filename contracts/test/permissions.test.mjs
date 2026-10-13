// Страж x-permission и x-idempotency контракта admin (спека сотрудников §2.4, §8; решения D14, D15):
// подсадка каждого нарушения на минимальном документе и на собранном бандле admin.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { permissionViolations } from '../src/permissions.mjs';

const bundle = parse(await readFile(new URL('../openapi/admin.yaml', import.meta.url), 'utf8'));
const catalog = JSON.parse(
  await readFile(new URL('../openapi/admin/permissions.json', import.meta.url), 'utf8'),
);

// Каталог-образец: служебные значения, обычное и изменяющее право, черновое.
const fixture = {
  service: ['anonymous', 'session', 'self', 'handler'],
  permissions: [
    { key: 'teams.teams.read', draft: false, mutating: false },
    { key: 'teams.teams.manage', draft: false, mutating: true },
    { key: 'teams.teams.transfer', draft: false, mutating: true },
    { key: 'teams.captains.approve', draft: true, mutating: true },
  ],
};

const base = () => ({
  openapi: '3.0.3',
  security: [{ session: [] }],
  paths: {
    '/v1/teams': {
      get: {
        operationId: 'listTeams',
        'x-permission': 'teams.teams.read',
        responses: { 200: { $ref: '#/components/responses/Teams' } },
      },
      post: {
        operationId: 'createTeam',
        'x-permission': 'handler',
        'x-permission-any': ['teams.teams.manage', 'teams.teams.transfer'],
        responses: { 202: { description: 'Принято' } },
      },
    },
    '/v1/auth/login': {
      post: {
        operationId: 'login',
        security: [],
        'x-permission': 'anonymous',
        responses: { 204: { description: 'Вошли' } },
      },
    },
    '/v1/me/mfa/setup': {
      post: {
        operationId: 'setupMfa',
        'x-permission': 'session',
        'x-idempotency': 'no-store',
        responses: {
          200: {
            description: 'Секрет',
            content: { 'application/json': { schema: { $ref: '#/components/schemas/Secret' } } },
          },
        },
      },
    },
  },
  components: {
    responses: {
      Teams: {
        description: 'Команды',
        content: { 'application/json': { schema: { $ref: '#/components/schemas/TeamPage' } } },
      },
    },
    schemas: {
      TeamPage: {
        type: 'object',
        properties: { items: { type: 'array', items: { $ref: '#/components/schemas/Team' } } },
      },
      Team: { type: 'object', properties: { name: { type: 'string' } } },
      // секрет глубже: allOf → свойство-объект → массив
      Secret: {
        allOf: [
          {
            type: 'object',
            properties: {
              setup: {
                type: 'object',
                properties: {
                  codes: { type: 'object', properties: { secret: { type: 'string' } } },
                },
              },
            },
          },
        ],
      },
    },
  },
});

const op = (d, path, verb) => d.paths[path][verb];
const list = (d) => op(d, '/v1/teams', 'get');
const create = (d) => op(d, '/v1/teams', 'post');
const login = (d) => op(d, '/v1/auth/login', 'post');
const setup = (d) => op(d, '/v1/me/mfa/setup', 'post');

test('эталонный документ без нарушений', () => {
  assert.deepEqual(permissionViolations(base(), fixture), []);
});

test('собранный контракт admin соблюдает правила x-permission и x-idempotency', () => {
  assert.deepEqual(permissionViolations(bundle, catalog), []);
});

const planted = {
  'нет x-permission': [
    /GET \/v1\/teams: нет x-permission/,
    (d) => {
      delete list(d)['x-permission'];
    },
  ],
  'пустой x-permission': [
    /GET \/v1\/teams: нет x-permission/,
    (d) => {
      list(d)['x-permission'] = '';
    },
  ],
  'право не из каталога': [
    /x-permission «teams\.teams\.list» — нет в каталоге/,
    (d) => {
      list(d)['x-permission'] = 'teams.teams.list';
    },
  ],
  'черновое право': [
    /x-permission «teams\.captains\.approve» — черновое/,
    (d) => {
      create(d)['x-permission'] = 'teams.captains.approve';
      delete create(d)['x-permission-any'];
    },
  ],
  'handler без x-permission-any': [
    /POST \/v1\/teams: x-permission: handler без x-permission-any/,
    (d) => {
      delete create(d)['x-permission-any'];
    },
  ],
  'handler с пустым x-permission-any': [
    /POST \/v1\/teams: x-permission: handler без x-permission-any/,
    (d) => {
      create(d)['x-permission-any'] = [];
    },
  ],
  'x-permission-any не список': [
    /POST \/v1\/teams: x-permission: handler без x-permission-any/,
    (d) => {
      create(d)['x-permission-any'] = 'teams.teams.manage';
    },
  ],
  'ключ x-permission-any не из каталога': [
    /x-permission-any: «teams\.teams\.delete» — нет в каталоге/,
    (d) => {
      create(d)['x-permission-any'] = ['teams.teams.manage', 'teams.teams.delete'];
    },
  ],
  'черновой ключ в x-permission-any': [
    /x-permission-any: «teams\.captains\.approve» — черновое/,
    (d) => {
      create(d)['x-permission-any'] = ['teams.captains.approve'];
    },
  ],
  'служебное значение в x-permission-any': [
    /x-permission-any: «self» — нет в каталоге/,
    (d) => {
      create(d)['x-permission-any'] = ['self'];
    },
  ],
  'x-permission-any без handler': [
    /GET \/v1\/teams: x-permission-any — только при x-permission: handler/,
    (d) => {
      list(d)['x-permission-any'] = ['teams.teams.read'];
    },
  ],
  'anonymous без security: []': [
    /POST \/v1\/auth\/login: x-permission: anonymous — только с security: \[\]/,
    (d) => {
      delete login(d).security;
    },
  ],
  'security: [] без anonymous': [
    /POST \/v1\/auth\/login: security: \[\] — только с x-permission: anonymous/,
    (d) => {
      login(d)['x-permission'] = 'session';
    },
  ],
  'секрет в ответе без x-idempotency: no-store': [
    /POST \/v1\/me\/mfa\/setup: в ответе поле secret — нужен x-idempotency: no-store/,
    (d) => {
      delete setup(d)['x-idempotency'];
    },
  ],
  'otpauth_uri в ответе по ссылке на ответ без no-store': [
    /GET \/v1\/teams: в ответе поле otpauth_uri — нужен x-idempotency: no-store/,
    (d) => {
      d.components.schemas.Team.properties.otpauth_uri = { type: 'string' };
    },
  ],
  'recovery_codes в элементе массива без no-store': [
    /GET \/v1\/teams: в ответе поле recovery_codes — нужен x-idempotency: no-store/,
    (d) => {
      d.components.schemas.Team.properties.recovery = {
        type: 'array',
        items: { type: 'object', properties: { recovery_codes: { type: 'array' } } },
      };
    },
  ],
  'x-idempotency не no-store': [
    /POST \/v1\/me\/mfa\/setup: x-idempotency «store» — допустимо только no-store/,
    (d) => {
      setup(d)['x-idempotency'] = 'store';
    },
  ],
  'x-idempotency с опечаткой у операции без секретов': [
    /POST \/v1\/teams: x-idempotency «nostore» — допустимо только no-store/,
    (d) => {
      create(d)['x-idempotency'] = 'nostore';
    },
  ],
  'GET с изменяющим правом': [
    /GET \/v1\/teams: GET с изменяющим правом teams\.teams\.manage/,
    (d) => {
      list(d)['x-permission'] = 'teams.teams.manage';
    },
  ],
  'GET handler с изменяющим правом в x-permission-any': [
    /GET \/v1\/teams: GET с изменяющим правом teams\.teams\.transfer/,
    (d) => {
      list(d)['x-permission'] = 'handler';
      list(d)['x-permission-any'] = ['teams.teams.read', 'teams.teams.transfer'];
    },
  ],
};

for (const [name, [rule, plant]] of Object.entries(planted)) {
  test(`ловит: ${name}`, () => {
    const d = base();
    plant(d);
    const got = permissionViolations(d, fixture);
    assert.ok(
      got.some((v) => rule.test(v)),
      `нет нарушения ${rule}: ${JSON.stringify(got)}`,
    );
  });
}

test('циклическая ссылка схемы не зацикливает обход', () => {
  const d = base();
  d.components.schemas.Team.properties.parent = { $ref: '#/components/schemas/Team' };
  assert.deepEqual(permissionViolations(d, fixture), []);
});

// Подсадка на собранном бандле: страж видит нарушения в настоящем контракте, а не только в образце.
const real = (id) => {
  const d = structuredClone(bundle);
  for (const item of Object.values(d.paths)) {
    for (const o of Object.values(item)) if (o?.operationId === id) return [d, o];
  }
  throw new Error(`нет операции ${id}`);
};

const plantedReal = {
  'staffMfaSetup без no-store': [
    'staffMfaSetup',
    /в ответе поле (secret|otpauth_uri) — нужен x-idempotency: no-store/,
    (o) => {
      delete o['x-idempotency'];
    },
  ],
  'regenerateStaffRecoveryCodes без no-store': [
    'regenerateStaffRecoveryCodes',
    /в ответе поле recovery_codes — нужен x-idempotency: no-store/,
    (o) => {
      delete o['x-idempotency'];
    },
  ],
  'getStaffMe без x-permission': [
    'getStaffMe',
    /GET \/v1\/staff\/me: нет x-permission/,
    (o) => {
      delete o['x-permission'];
    },
  ],
  'staffLogin не анонимная': [
    'staffLogin',
    /security: \[\] — только с x-permission: anonymous/,
    (o) => {
      o['x-permission'] = 'session';
    },
  ],
  'createStaffInvite без x-permission-any': [
    'createStaffInvite',
    /handler без x-permission-any/,
    (o) => {
      delete o['x-permission-any'];
    },
  ],
  'listAudit с изменяющим правом': [
    'listAudit',
    /GET с изменяющим правом geo\.imports\.run/,
    (o) => {
      o['x-permission'] = 'geo.imports.run';
    },
  ],
  'approveApproval с черновым правом': [
    'approveApproval',
    /x-permission-any: «moderation\.bans\.issue» — черновое/,
    (o) => {
      o['x-permission-any'] = [...o['x-permission-any'], 'moderation.bans.issue'];
    },
  ],
};

for (const [name, [id, rule, plant]] of Object.entries(plantedReal)) {
  test(`ловит в бандле admin: ${name}`, () => {
    const [d, o] = real(id);
    plant(o);
    const got = permissionViolations(d, catalog);
    assert.ok(
      got.some((v) => rule.test(v)),
      `нет нарушения ${rule}: ${JSON.stringify(got)}`,
    );
  });
}
