import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { docViolations } from '../src/docs.mjs';

const load = async (name) =>
  parse(await readFile(new URL(`../openapi/${name}.yaml`, import.meta.url), 'utf8'));

// Swagger (/docs) рисуется из бандла: без описаний и примеров нативщику и вебу нечего читать.
for (const name of ['public', 'admin']) {
  test(`${name}.yaml полностью описан — Swagger без вопросов`, async () => {
    assert.deepEqual(docViolations(await load(name)), []);
  });
}

const base = () => ({
  openapi: '3.0.3',
  info: { title: 'T', version: '0.1.0', description: 'API для тестов' },
  tags: [{ name: 'teams', description: 'Команды' }],
  paths: {
    '/v1/teams/{teamId}': {
      get: {
        operationId: 'getTeam',
        tags: ['teams'],
        summary: 'Команда',
        description: 'Карточка команды по id',
        parameters: [
          {
            name: 'teamId',
            in: 'path',
            required: true,
            description: 'Id команды',
            schema: { type: 'string', format: 'uuid' },
            example: '0b9c7c7e-5d1a-4c55-9d1e-0f6b2f6c1a11',
          },
          { $ref: '#/components/parameters/Lang' },
        ],
        responses: {
          200: {
            description: 'Команда',
            content: { 'application/json': { schema: { $ref: '#/components/schemas/Team' } } },
          },
          default: { $ref: '#/components/responses/Problem' },
        },
      },
      patch: {
        operationId: 'renameTeam',
        tags: ['teams'],
        summary: 'Переименовать',
        description: 'Новое название команды',
        requestBody: {
          description: 'Новое название',
          required: true,
          content: {
            'application/json': {
              schema: {
                type: 'object',
                properties: {
                  name: { type: 'string', description: 'Название', example: 'Дворовые' },
                },
              },
            },
          },
        },
        responses: { 204: { description: 'Переименована' } },
      },
    },
  },
  components: {
    parameters: {
      Lang: {
        name: 'lang',
        in: 'query',
        description: 'Язык ответа',
        schema: { type: 'string', example: 'ru' },
      },
    },
    responses: {
      Problem: {
        description: 'Ошибка',
        content: {
          'application/problem+json': { schema: { $ref: '#/components/schemas/Problem' } },
        },
      },
    },
    schemas: {
      Team: {
        type: 'object',
        description: 'Команда',
        properties: {
          id: {
            type: 'string',
            description: 'Id',
            example: '0b9c7c7e-5d1a-4c55-9d1e-0f6b2f6c1a11',
          },
          surface: { type: 'string', description: 'Покрытие', enum: ['grass', 'rubber'] },
          captain: { $ref: '#/components/schemas/Player' },
          tags: {
            type: 'array',
            description: 'Метки',
            items: { type: 'string' },
            example: ['вечер'],
          },
          roster: {
            type: 'array',
            description: 'Состав',
            items: {
              type: 'object',
              properties: { number: { type: 'integer', description: 'Номер', example: 10 } },
            },
          },
        },
      },
      Player: {
        type: 'object',
        description: 'Игрок',
        properties: { nick: { type: 'string', description: 'Ник', example: 'Kolya' } },
      },
      Problem: {
        type: 'object',
        description: 'Ошибка RFC 9457',
        properties: { code: { type: 'string', description: 'Код', example: 'http.not_found' } },
      },
    },
  },
});

const get = (d) => d.paths['/v1/teams/{teamId}'].get;
const patch = (d) => d.paths['/v1/teams/{teamId}'].patch;
const team = (d) => d.components.schemas.Team;

test('эталонный документ без нарушений', () => {
  assert.deepEqual(docViolations(base()), []);
});

// Подсадка багов: каждое недоописанное место ловится своим правилом.
const planted = {
  'нет описания API': [
    /^info\.description/,
    (d) => {
      delete d.info.description;
    },
  ],
  'тег операции не объявлен': [
    /тег «matches»/,
    (d) => {
      get(d).tags = ['matches'];
    },
  ],
  'тег без описания': [
    /тег «teams»/,
    (d) => {
      delete d.tags[0].description;
    },
  ],
  'операция без summary': [
    /GET \/v1\/teams\/\{teamId\}: нет summary/,
    (d) => {
      delete get(d).summary;
    },
  ],
  'операция без description': [
    /GET \/v1\/teams\/\{teamId\}: нет description/,
    (d) => {
      get(d).description = '  ';
    },
  ],
  'параметр без описания': [
    /параметр «teamId»: нет description/,
    (d) => {
      delete get(d).parameters[0].description;
    },
  ],
  'параметр без примера': [
    /параметр «teamId»: нет примера/,
    (d) => {
      delete get(d).parameters[0].example;
    },
  ],
  'параметр-компонент без примера': [
    /параметр «lang»: нет примера/,
    (d) => {
      delete d.components.parameters.Lang.schema.example;
    },
  ],
  'тело запроса без описания': [
    /PATCH \/v1\/teams\/\{teamId\}: тело запроса — нет description/,
    (d) => {
      delete patch(d).requestBody.description;
    },
  ],
  'поле тела запроса без примера': [
    /PATCH \/v1\/teams\/\{teamId\}: тело запроса\.name: нет примера/,
    (d) => {
      delete patch(d).requestBody.content['application/json'].schema.properties.name.example;
    },
  ],
  'ответ без описания': [
    /ответ 204: нет description/,
    (d) => {
      patch(d).responses[204].description = '';
    },
  ],
  'схема без описания': [
    /схема Player: нет description/,
    (d) => {
      delete d.components.schemas.Player.description;
    },
  ],
  'поле схемы без описания': [
    /схема Team\.id: нет description/,
    (d) => {
      delete team(d).properties.id.description;
    },
  ],
  'поле схемы без примера': [
    /схема Team\.id: нет примера/,
    (d) => {
      delete team(d).properties.id.example;
    },
  ],
  'массив простых значений без примера': [
    /схема Team\.tags: нет примера/,
    (d) => {
      delete team(d).properties.tags.example;
    },
  ],
  'поле элемента массива без примера': [
    /схема Team\.roster\[\]\.number: нет примера/,
    (d) => {
      delete team(d).properties.roster.items.properties.number.example;
    },
  ],
  'поле внутри allOf без описания': [
    /схема Team\.extra: нет description/,
    (d) => {
      d.components.schemas.Team = {
        description: 'Команда',
        allOf: [
          team(d),
          { type: 'object', properties: { extra: { type: 'string', example: 'x' } } },
        ],
      };
    },
  ],
  'простая схема без примера': [
    /схема Surface: нет примера/,
    (d) => {
      d.components.schemas.Surface = { type: 'string', description: 'Покрытие' };
    },
  ],
};
for (const [name, [rule, plant]] of Object.entries(planted)) {
  test(`ловит: ${name}`, () => {
    const d = base();
    plant(d);
    const got = docViolations(d);
    assert.ok(
      got.some((v) => rule.test(v)),
      `нет нарушения ${rule}: ${JSON.stringify(got)}`,
    );
  });
}

test('пример в examples тоже считается', () => {
  const d = base();
  const p = get(d).parameters[0];
  delete p.example;
  p.examples = { one: { value: '0b9c7c7e-5d1a-4c55-9d1e-0f6b2f6c1a11' } };
  assert.deepEqual(docViolations(d), []);
});

test('пример у элементов массива заменяет пример массива', () => {
  const d = base();
  const tags = team(d).properties.tags;
  delete tags.example;
  tags.items.example = 'вечер';
  assert.deepEqual(docViolations(d), []);
});
