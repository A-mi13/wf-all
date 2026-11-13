import { describe, expect, test } from 'vitest';
import { KEYS, parseFlagsSearch, toCitiesQuery } from './search';

// Фильтры «Флагов функций» в адресе (/flags?q=&status=&flag=): поиск и статус города сужают столбцы матрицы
// (на телефоне — города раскрытого флага), flag — раскрытый флаг телефона. Негодное отбрасывается.
describe('parseFlagsSearch', () => {
  test('ключи пути — q, status, flag', () => {
    expect(KEYS).toEqual(['q', 'status', 'flag']);
  });

  test('годные значения проходят', () => {
    expect(parseFlagsSearch({ q: 'Став', status: 'pilot', flag: 'city_tables' })).toEqual({
      q: 'Став',
      status: 'pilot',
      flag: 'city_tables',
    });
  });

  test('негодное отброшено: статус не из контракта, пустой поиск, ключ флага не по шаблону', () => {
    expect(parseFlagsSearch({ q: '   ', status: 'closed', flag: 'Bad-Key' })).toEqual({});
    expect(parseFlagsSearch({ status: ['live'], flag: 1 })).toEqual({});
  });

  test('поиск: число из JSON-разбора роутера — строкой, длиннее 100 — обрезан', () => {
    expect(parseFlagsSearch({ q: 123 })).toEqual({ q: '123' });
    expect(parseFlagsSearch({ q: 'а'.repeat(150) }).q).toHaveLength(100);
  });

  test('в запрос городов — поиск без пробелов по краям и статус массивом', () => {
    expect(toCitiesQuery({ q: ' Став ', status: 'live' })).toEqual({ q: 'Став', status: ['live'] });
    expect(toCitiesQuery({ flag: 'ads' })).toEqual({});
  });
});
