import { describe, expect, test } from 'vitest';
import ru from '../../../messages/ru.json';
import { AUDIT_SECTIONS, dayStartUtc, parseAuditSearch, toAuditQuery } from './search';

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

  // Ревью M8: действие из другого раздела, чем вкладка, — отбрасывается (вкладка видна, а фильтр
  // молча искал бы чужое).
  test('действие не из раздела вкладки — отброшено', () => {
    expect(parseAuditSearch({ tab: 'staff', action: 'platform.approval_executed' })).toEqual({
      tab: 'staff',
    });
    expect(parseAuditSearch({ tab: 'staff', action: 'staff.login' })).toEqual({
      tab: 'staff',
      action: 'staff.login',
    });
    expect(parseAuditSearch({ action: 'platform.approval_executed' })).toEqual({
      action: 'platform.approval_executed',
    });
  });

  // Ревью уборки №7 m3: action на «.» или «_» сервер понимает как начало строки (префикс), а фильтр
  // «Действие» — только точный код; пустой сегмент — не код. Такое действие из адреса отбрасывается.
  test('действие-префикс (на «.» или «_») и пустой сегмент — отброшены', () => {
    expect(parseAuditSearch({ action: 'staff.login_' })).toEqual({});
    expect(parseAuditSearch({ action: 'staff.' })).toEqual({});
    expect(parseAuditSearch({ action: 'platform.approval_' })).toEqual({});
    expect(parseAuditSearch({ action: 'staff..login' })).toEqual({});
    expect(parseAuditSearch({ action: 'staff..' })).toEqual({});
    expect(parseAuditSearch({ tab: 'staff', action: 'staff.login_' })).toEqual({ tab: 'staff' });
    expect(parseAuditSearch({ action: 'staff.login_failed' })).toEqual({
      action: 'staff.login_failed',
    });
  });

  // Уборка №8 m2: односегментный код («staff») сервер понял бы как всё действие и его подвиды через
  // точку — то есть весь модуль, а фильтр «Действие» показал бы его как точный код. Код — минимум два
  // сегмента.
  test('односегментное действие — отброшено', () => {
    expect(parseAuditSearch({ action: 'staff' })).toEqual({});
    expect(parseAuditSearch({ action: 'geo' })).toEqual({});
    expect(parseAuditSearch({ tab: 'staff', action: 'staff' })).toEqual({ tab: 'staff' });
    expect(parseAuditSearch({ action: 'geo.city.rename' })).toEqual({ action: 'geo.city.rename' });
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

  // Ревью M3: перевод часов вперёд ровно в полночь — местной полуночи нет; «с» — первый
  // существующий момент дня (01:00), а не час предыдущего дня.
  test('полуночи нет (перевод вперёд в 00:00) — первый момент дня', () => {
    // Сантьяго: 6 сентября 2026, 00:00 −04 → 01:00 −03 (04:00 UTC)
    expect(dayStartUtc('2026-09-06', 'America/Santiago')).toBe('2026-09-06T04:00:00.000Z');
    // «по» 5 сентября включительно — до первого момента 6-го
    expect(dayStartUtc('2026-09-05', 'America/Santiago', 1)).toBe('2026-09-06T04:00:00.000Z');
    // Гавана: 8 марта 2026, 00:00 −05 → 01:00 −04 (05:00 UTC)
    expect(dayStartUtc('2026-03-08', 'America/Havana')).toBe('2026-03-08T05:00:00.000Z');
    // соседние дни — обычная полночь
    expect(dayStartUtc('2026-09-07', 'America/Santiago')).toBe('2026-09-07T03:00:00.000Z');
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

  // Правило сервера для action_prefix (контракт, 305026c): оканчивается на «.» или «_» — начало
  // строки; иначе — само действие и его подвиды через точку. Вкладка — раздел целиком (начало строки),
  // фильтр «Действие» — точный код из подписей audit.actions (не начало: staff.login ≠ staff.login_failed).
  test('вкладки — начало строки, коды фильтра «Действие» — точные действия', () => {
    for (const prefix of Object.values(AUDIT_SECTIONS)) expect(prefix).toMatch(/[._]$/);
    const codes = (tree: object, at = ''): string[] =>
      Object.entries(tree).flatMap(([k, v]) =>
        typeof v === 'string' ? [`${at}${k}`] : codes(v as object, `${at}${k}.`),
      );
    const actions = codes(ru.audit.actions);
    expect(actions).toContain('staff.login');
    for (const code of actions) expect(code).not.toMatch(/[._]$/);
  });
});
