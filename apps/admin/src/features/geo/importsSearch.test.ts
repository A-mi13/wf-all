import { describe, expect, test } from 'vitest';
import { KEYS, parseImportsSearch, toImportsQuery } from './importsSearch';

// Фильтры «Импорта GeoNames» (план B, Task 20; спека UI-фундамента §6.2, U18): country — страна журнала, job —
// строка задачи очереди (F5 и пересланная ссылка возобновляют её опрос). Негодное отбрасывается.
describe('parseImportsSearch', () => {
  test('ключи — country и job, по порядку', () => {
    expect(KEYS).toEqual(['country', 'job']);
  });

  test('годные значения: страна ISO, задача — положительное целое (роутер отдаёт числом или строкой)', () => {
    expect(parseImportsSearch({ country: 'RU', job: 1842 })).toEqual({ country: 'RU', job: 1842 });
    expect(parseImportsSearch({ job: '1842' })).toEqual({ job: 1842 });
  });

  test.each([
    ['страна строчными', { country: 'ru' }],
    ['страна из трёх букв', { country: 'RUS' }],
    ['страна не строка', { country: 7 }],
    ['задача 0', { job: 0 }],
    ['задача отрицательная', { job: -5 }],
    ['задача дробная', { job: 1.5 }],
    ['задача больше 2^53', { job: 2 ** 53 }],
    ['задача — строка не из цифр', { job: '12a' }],
    ['задача — длинная строка', { job: '99999999999999999999' }],
    ['задача — массив', { job: [1] }],
  ])('%s — отброшено', (_, input) => {
    expect(parseImportsSearch(input)).toEqual({});
  });

  test('посторонние ключи не проходят', () => {
    expect(parseImportsSearch({ country: 'KZ', start: 1, import: 'x', foo: 'y' })).toEqual({
      country: 'KZ',
    });
  });
});

test('toImportsQuery — только страна (задача — строка над журналом, не фильтр списка)', () => {
  expect(toImportsQuery({ country: 'RU', job: 5 })).toEqual({ country: 'RU' });
  expect(toImportsQuery({})).toEqual({});
});
