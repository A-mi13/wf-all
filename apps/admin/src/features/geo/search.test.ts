import { describe, expect, test } from 'vitest';
import { parseCitiesSearch, toCitiesQuery } from './search';

// Фильтры «Городов и запуска» — search-параметры /geo/cities (спека §9.1): q, status (массив),
// country, region_id. Негодное из адреса отбрасывается — в запрос уходят только значения контракта.

const REGION = 'b7e4d2c9-1a3f-4e8b-9c6d-5f0a2e7b1c38';

describe('parseCitiesSearch', () => {
  test('знакомые значения — как есть', () => {
    expect(
      parseCitiesSearch({ q: 'став', status: ['pilot', 'live'], country: 'RU', region_id: REGION }),
    ).toEqual({ q: 'став', status: ['pilot', 'live'], country: 'RU', region_id: REGION });
  });

  test('один статус строкой (ссылка руками) — массив из одного', () => {
    expect(parseCitiesSearch({ status: 'live' })).toEqual({ status: ['live'] });
  });

  test('мусор отброшен: статус не из списка, страна не ISO, регион не uuid, пустой поиск', () => {
    expect(
      parseCitiesSearch({
        q: '   ',
        status: ['archived', 'constructor', 7],
        country: 'ru',
        region_id: 'not-a-uuid',
      }),
    ).toEqual({});
    expect(parseCitiesSearch({ country: 'RUS', status: {} })).toEqual({});
  });

  test('повторы статуса схлопнуты, порядок сохранён', () => {
    expect(parseCitiesSearch({ status: ['live', 'live', 'pilot'] })).toEqual({
      status: ['live', 'pilot'],
    });
  });

  test('число в поиске (роутер разбирает JSON) — строкой; длиннее 100 — обрезано', () => {
    expect(parseCitiesSearch({ q: 123 })).toEqual({ q: '123' });
    expect(parseCitiesSearch({ q: 'а'.repeat(150) }).q).toHaveLength(100);
  });
});

describe('toCitiesQuery', () => {
  test('поиск обрезан по краям; пустые поля не уходят', () => {
    expect(toCitiesQuery({ q: '  став ', status: ['live'] })).toEqual({
      q: 'став',
      status: ['live'],
    });
    expect(toCitiesQuery({})).toEqual({});
    expect(toCitiesQuery({ status: [] })).toEqual({});
  });
});
