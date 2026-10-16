import { describe, expect, test } from 'vitest';
import { dayStartUtc, parseAuditSearch, toAuditQuery } from './search';

// Фильтры журнала — search-параметры /audit (спека §9.1, §5.3): незнакомое и негодное из адреса
// отбрасывается (в запрос уходят только значения контракта); период — даты в местном времени
// сотрудника → from/to в UTC (правило 3): «с» — местная полночь дня, «по» — включительно, то есть
// местная полночь следующего дня (to у сервера — «не включая»).

const STAFF = '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d';
const OBJ = '0192f1c4-1111-7b21-9c5d-000000000001';

describe('parseAuditSearch', () => {
  test('знакомые значения — как есть', () => {
    expect(
      parseAuditSearch({
        tab: 'staff',
        action: 'staff.login',
        actor: STAFF,
        object_type: 'staff_account',
        object_id: OBJ,
        from: '2026-10-01',
        to: '2026-10-13',
      }),
    ).toEqual({
      tab: 'staff',
      action: 'staff.login',
      actor: STAFF,
      object_type: 'staff_account',
      object_id: OBJ,
      from: '2026-10-01',
      to: '2026-10-13',
    });
  });

  test('мусор отброшен: вкладка, id не uuid, дата не дата, код с шаблоном', () => {
    expect(
      parseAuditSearch({
        tab: 'all',
        action: 'staff.%',
        actor: 'not-a-uuid',
        object_type: 'Staff Account',
        object_id: 42,
        from: '2026-02-30',
        to: '13.10.2026',
      }),
    ).toEqual({});
    expect(parseAuditSearch({ tab: 'flags; drop' })).toEqual({});
  });

  test('«по» раньше «с» — «по» отброшен (сервер ответил бы 400)', () => {
    expect(parseAuditSearch({ from: '2026-10-13', to: '2026-10-01' })).toEqual({
      from: '2026-10-13',
    });
    expect(parseAuditSearch({ from: '2026-10-13', to: '2026-10-13' })).toEqual({
      from: '2026-10-13',
      to: '2026-10-13',
    });
  });
});

describe('период → UTC', () => {
  test('местная полночь в двух поясах', () => {
    expect(dayStartUtc('2026-10-13', 'Europe/Moscow')).toBe('2026-10-12T21:00:00.000Z');
    expect(dayStartUtc('2026-10-13', 'America/New_York')).toBe('2026-10-13T04:00:00.000Z');
    expect(dayStartUtc('2026-10-13', 'UTC')).toBe('2026-10-13T00:00:00.000Z');
  });

  test('переход на зимнее время: полночь берётся по смещению того дня', () => {
    // США: летнее время кончается 1 ноября 2026 — 31 октября UTC−4, 2 ноября UTC−5
    expect(dayStartUtc('2026-10-31', 'America/New_York')).toBe('2026-10-31T04:00:00.000Z');
    expect(dayStartUtc('2026-11-02', 'America/New_York')).toBe('2026-11-02T05:00:00.000Z');
  });

  test('переход между местной и UTC-полуночью: смещение пересчитывается на сам момент', () => {
    // Сидней: летнее время с 4 октября 2026, 02:00 (+10 → +11, 3 окт. 16:00 UTC). Полночь 4 октября —
    // ещё +10, а 4 окт. 00:00 UTC — уже +11: первая оценка ошиблась бы на час.
    expect(dayStartUtc('2026-10-04', 'Australia/Sydney')).toBe('2026-10-03T14:00:00.000Z');
    expect(dayStartUtc('2026-10-05', 'Australia/Sydney')).toBe('2026-10-04T13:00:00.000Z');
  });

  test('toAuditQuery: «по» включительно, вкладка → action_prefix, действие важнее вкладки', () => {
    expect(
      toAuditQuery({ tab: 'staff', from: '2026-10-01', to: '2026-10-13' }, 'Europe/Moscow'),
    ).toEqual({
      action_prefix: 'staff.',
      from: '2026-09-30T21:00:00.000Z',
      to: '2026-10-13T21:00:00.000Z',
    });
    expect(toAuditQuery({ tab: 'staff', action: 'staff.login', actor: STAFF }, 'UTC')).toEqual({
      action_prefix: 'staff.login',
      actor_staff_id: STAFF,
    });
    expect(toAuditQuery({ to: '2026-12-31' }, 'UTC')).toEqual({ to: '2027-01-01T00:00:00.000Z' });
    expect(toAuditQuery({}, 'UTC')).toEqual({});
  });

  test.each([
    ['approvals', 'platform.approval_'],
    ['directories', 'geo.'],
    ['flags', 'flags.'],
  ] as const)('вкладка %s → %s', (tab, prefix) => {
    expect(toAuditQuery({ tab }, 'UTC')).toEqual({ action_prefix: prefix });
  });
});
