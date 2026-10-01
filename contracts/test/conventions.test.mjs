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

const base = () => ({
  openapi: '3.0.3',
  security: [{ bearer: [] }],
  'x-error-codes-common': ['internal'],
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
  components: { schemas: { Team: { type: 'object' } } },
});

test('эталонный документ без нарушений', () => {
  assert.deepEqual(conventionViolations(base()), []);
});

// Подсадка багов: каждое нарушение конвенций ловится.
const planted = {
  'мутирующая операция с аутентификацией без Idempotency-Key': (d) => {
    d.paths['/v1/teams'].post.parameters = [];
  },
  'два тега': (d) => {
    d.paths['/v1/teams'].post.tags = ['teams', 'matches'];
  },
  'operationId в snake_case': (d) => {
    d.paths['/v1/teams'].post.operationId = 'create_team';
  },
  'нет x-error-codes': (d) => {
    delete d.paths['/v1/teams'].post['x-error-codes'];
  },
  'код ошибки не по формату': (d) => {
    d.paths['/v1/teams'].post['x-error-codes'] = ['Teams.NameTaken'];
  },
  'внешний $ref — контракт не собран': (d) => {
    d.components.schemas.Team = { $ref: './teams/schemas.yaml#/Team' };
  },
  'схема, переименованная redocly': (d) => {
    d.components.schemas['Team-2'] = { type: 'object' };
  },
  'нет x-error-codes-common': (d) => {
    delete d['x-error-codes-common'];
  },
};
for (const [name, plant] of Object.entries(planted)) {
  test(`ловит: ${name}`, () => {
    const d = base();
    plant(d);
    assert.notDeepEqual(conventionViolations(d), []);
  });
}

test('анонимная мутирующая операция обходится без Idempotency-Key', () => {
  const d = base();
  Object.assign(d.paths['/v1/teams'].post, { security: [], parameters: [] });
  assert.deepEqual(conventionViolations(d), []);
});

test('errorCodes — общие коды и коды операций без повторов', () => {
  const d = base();
  d.paths['/v1/x'] = { get: { 'x-error-codes': ['teams.name_taken', 'teams.not_found'] } };
  assert.deepEqual(errorCodes(d), ['internal', 'teams.name_taken', 'teams.not_found']);
});
