import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { conventionViolations, errorCodes } from '../src/conventions.mjs';

const load = async (name) =>
  parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));

for (const name of ['public', 'admin']) {
  test(`${name}.yaml соблюдает конвенции контракта`, async () => {
    assert.deepEqual(conventionViolations(await load(name)), []);
  });
}

test('Problem описывает ошибки полей', async () => {
  const errors = (await load('public')).components.schemas.Problem.properties.errors;
  assert.deepEqual(errors.items.required, ['field', 'code']);
});

// Спека §6.4: в админке Idempotency-Key обязателен у всех мутирующих операций.
test('в админке нет анонимных мутирующих операций', async () => {
  assert.deepEqual((await load('admin'))['x-anonymous-mutations'], []);
});

const base = () => ({
  openapi: '3.0.3',
  security: [{ bearer: [] }],
  'x-error-codes-common': ['internal'],
  'x-anonymous-mutations': [],
  paths: {
    '/v1/teams': {
      post: {
        operationId: 'createTeam',
        tags: ['teams'],
        'x-error-codes': ['teams.name_taken'],
        parameters: [{ $ref: '#/components/parameters/IdempotencyKey' }],
        responses: {},
      },
    },
  },
  components: {
    parameters: {
      IdempotencyKey: {
        name: 'Idempotency-Key',
        in: 'header',
        required: true,
        schema: { type: 'string' },
      },
    },
    schemas: { Team: { type: 'object' } },
  },
});

const post = (d) => d.paths['/v1/teams'].post;

test('эталонный документ без нарушений', () => {
  assert.deepEqual(conventionViolations(base()), []);
});

// Подсадка багов: каждое нарушение конвенций ловится своим правилом.
const planted = {
  'мутирующая операция с аутентификацией без Idempotency-Key': [
    /без Idempotency-Key/,
    (d) => {
      post(d).parameters = [];
    },
  ],
  'два тега': [
    /ровно один тег/,
    (d) => {
      post(d).tags = ['teams', 'matches'];
    },
  ],
  'operationId в snake_case': [
    /lowerCamel/,
    (d) => {
      post(d).operationId = 'create_team';
    },
  ],
  'нет x-error-codes': [
    /нет x-error-codes$/,
    (d) => {
      delete post(d)['x-error-codes'];
    },
  ],
  'код ошибки не по формату': [
    /код ошибки «Teams\.NameTaken»/,
    (d) => {
      post(d)['x-error-codes'] = ['Teams.NameTaken'];
    },
  ],
  'код ошибки без модуля': [
    /код ошибки «name_taken»/,
    (d) => {
      post(d)['x-error-codes'] = ['name_taken'];
    },
  ],
  'внешний $ref — контракт не собран': [
    /внешний \$ref/,
    (d) => {
      d.components.schemas.Team = { $ref: './teams/schemas.yaml#/Team' };
    },
  ],
  'схема, переименованная redocly': [
    /redocly/,
    (d) => {
      d.components.schemas['Team-2'] = { type: 'object' };
    },
  ],
  'нет x-error-codes-common': [
    /нет x-error-codes-common/,
    (d) => {
      delete d['x-error-codes-common'];
    },
  ],
  'общий код ошибки не по формату': [
    /общий код ошибки «Bad Code»/,
    (d) => {
      d['x-error-codes-common'] = ['internal', 'Bad Code'];
    },
  ],
  'удалена корневая security': [
    /нет аутентификации по умолчанию/,
    (d) => {
      delete d.security;
    },
  ],
  'пустая корневая security': [
    /нет аутентификации по умолчанию/,
    (d) => {
      d.security = [];
    },
  ],
  'нет x-anonymous-mutations': [
    /нет x-anonymous-mutations/,
    (d) => {
      delete d['x-anonymous-mutations'];
    },
  ],
  'анонимная операция не из списка': [
    /не из x-anonymous-mutations/,
    (d) => {
      Object.assign(post(d), { security: [], parameters: [] });
    },
  ],
  'устаревшая запись в списке анонимных': [
    /x-anonymous-mutations: «createTeam»/,
    (d) => {
      d['x-anonymous-mutations'] = ['createTeam'];
    },
  ],
  'запись в списке анонимных без операции': [
    /x-anonymous-mutations: «signIn»/,
    (d) => {
      d['x-anonymous-mutations'] = ['signIn'];
    },
  ],
  'нет компонента IdempotencyKey': [
    /components\.parameters\.IdempotencyKey/,
    (d) => {
      delete d.components.parameters.IdempotencyKey;
    },
  ],
  'IdempotencyKey необязателен': [
    /components\.parameters\.IdempotencyKey/,
    (d) => {
      d.components.parameters.IdempotencyKey.required = false;
    },
  ],
  'IdempotencyKey не заголовок': [
    /components\.parameters\.IdempotencyKey/,
    (d) => {
      d.components.parameters.IdempotencyKey.in = 'query';
    },
  ],
  'IdempotencyKey под другим именем': [
    /components\.parameters\.IdempotencyKey/,
    (d) => {
      d.components.parameters.IdempotencyKey.name = 'X-Request-Key';
    },
  ],
};
for (const [name, [rule, plant]] of Object.entries(planted)) {
  test(`ловит: ${name}`, () => {
    const d = base();
    plant(d);
    const got = conventionViolations(d);
    assert.ok(
      got.some((v) => rule.test(v)),
      `нет нарушения ${rule}: ${JSON.stringify(got)}`,
    );
  });
}

for (const verb of ['put', 'patch', 'delete']) {
  test(`ловит: ${verb.toUpperCase()} без Idempotency-Key`, () => {
    const d = base();
    d.paths['/v1/teams'][verb] = { ...post(d), operationId: `${verb}Team`, parameters: [] };
    assert.deepEqual(conventionViolations(d), [
      `${verb.toUpperCase()} /v1/teams: мутирующая операция с аутентификацией без Idempotency-Key`,
    ]);
  });
}

test('GET обходится без Idempotency-Key', () => {
  const d = base();
  d.paths['/v1/teams'].get = { ...post(d), operationId: 'listTeams', parameters: [] };
  assert.deepEqual(conventionViolations(d), []);
});

test('security у операции не заменяет корневую и не отменяет Idempotency-Key', () => {
  const d = base();
  delete d.security;
  Object.assign(post(d), { security: [{ bearer: [] }], parameters: [] });
  const got = conventionViolations(d);
  assert.ok(
    got.some((v) => /нет аутентификации по умолчанию/.test(v)),
    JSON.stringify(got),
  );
  assert.ok(
    got.some((v) => /без Idempotency-Key/.test(v)),
    JSON.stringify(got),
  );
});

test('анонимная мутирующая операция из списка обходится без Idempotency-Key', () => {
  const d = base();
  d['x-anonymous-mutations'] = ['createTeam'];
  Object.assign(post(d), { security: [], parameters: [] });
  assert.deepEqual(conventionViolations(d), []);
});

test('errorCodes — общие коды и коды операций без повторов', () => {
  const d = base();
  d.paths['/v1/x'] = { get: { 'x-error-codes': ['teams.name_taken', 'teams.not_found'] } };
  assert.deepEqual(errorCodes(d), ['internal', 'teams.name_taken', 'teams.not_found']);
});
