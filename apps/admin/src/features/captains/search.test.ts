import { describe, expect, test } from 'vitest';
import { KEYS, parseCaptainsSearch } from './search';

// Фильтры «Капитанов» — search-параметры /captains (план I-4, Task 8): статус и город; незнакомое отбрасывается —
// в запрос уходят только значения контракта.
const CITY = '3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47';

describe('parseCaptainsSearch', () => {
  test('знакомые значения — как есть', () => {
    expect(parseCaptainsSearch({ status: 'rejected', city: CITY })).toEqual({
      status: 'rejected',
      city: CITY,
    });
  });

  test('незнакомый статус и город не uuid отбрасываются', () => {
    expect(parseCaptainsSearch({ status: 'expired', city: 'moscow' })).toEqual({});
    expect(parseCaptainsSearch({ status: 1, city: 7 })).toEqual({});
  });

  test('pending — по умолчанию, в адрес не пишется', () => {
    expect(parseCaptainsSearch({ status: 'pending' })).toEqual({});
  });

  test('ключи пути', () => {
    expect(KEYS).toEqual(['status', 'city']);
  });
});
