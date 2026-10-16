import type { operations } from '../../api/gen/admin';

// Фильтры «Журнала действий» — типизированные search-параметры маршрута /audit (спека §9.1, §5.3):
// переживают перезагрузку и «Назад». Незнакомое и негодное значение из адреса отбрасывается — в
// запрос уходят только значения, которые сервер примет (uuid, код действия без шаблонов, дата).
// Период — календарные дни в местном времени сотрудника; в запрос — моменты UTC (правило 3).

// Вкладки по разделам → начало кода действия (`<модуль>.<действие>`, действия аудита бэкенда:
// staff.* — модуль сотрудников, platform.approval_* — решения подтверждений, geo.* — справочники
// (города, импорт GeoNames); flags.* — флаги функций (план B, по ключам прав flags.*)).
export const AUDIT_SECTIONS = {
  staff: 'staff.',
  approvals: 'platform.approval_',
  directories: 'geo.',
  flags: 'flags.',
} as const;
export type AuditSection = keyof typeof AUDIT_SECTIONS;
export const SECTIONS: readonly AuditSection[] = ['staff', 'approvals', 'directories', 'flags'];

export type AuditSearch = {
  tab?: AuditSection; // без вкладки — «Все»
  action?: string; // код действия (или его начало) — важнее вкладки
  actor?: string; // id сотрудника-автора
  object_type?: string;
  object_id?: string;
  from?: string; // YYYY-MM-DD, включительно
  to?: string; // YYYY-MM-DD, включительно
};

export type AuditQuery = NonNullable<operations['listAudit']['parameters']['query']>;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const ACTION = /^[a-z][a-z0-9_]*(\.[a-z0-9_]*)*$/;
const OBJECT_TYPE = /^[a-z][a-z0-9_]{0,63}$/;
const DAY = /^(\d{4})-(\d{2})-(\d{2})$/;

export const isUuid = (v: string) => UUID.test(v);

// Настоящая календарная дата YYYY-MM-DD (2026-02-30 — нет).
export function isDay(v: string): boolean {
  const m = DAY.exec(v);
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const t = new Date(Date.UTC(y, mo - 1, d));
  return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d;
}

const isSection = (v: unknown): v is AuditSection =>
  typeof v === 'string' && Object.hasOwn(AUDIT_SECTIONS, v);
const str = (v: unknown, ok: (s: string) => boolean): string | undefined =>
  typeof v === 'string' && ok(v) ? v : undefined;

export function parseAuditSearch(s: Record<string, unknown>): AuditSearch {
  const out: AuditSearch = {};
  if (isSection(s.tab)) out.tab = s.tab;
  const action = str(s.action, (v) => v.length <= 100 && ACTION.test(v));
  // действие из другого раздела, чем вкладка, — не берём: вкладка видна, а искал бы чужое
  if (action && (!out.tab || action.startsWith(AUDIT_SECTIONS[out.tab]))) out.action = action;
  const actor = str(s.actor, isUuid);
  if (actor) out.actor = actor;
  const type = str(s.object_type, (v) => OBJECT_TYPE.test(v));
  if (type) out.object_type = type;
  const object = str(s.object_id, isUuid);
  if (object) out.object_id = object;
  const from = str(s.from, isDay);
  if (from) out.from = from;
  const to = str(s.to, isDay);
  // «по» раньше «с» — сервер ответил бы 400 (range): такой «по» не берём
  if (to && !(from && to < from)) out.to = to;
  return out;
}

// Смещение пояса timeZone от UTC в момент ts, мс (местное − UTC).
function offsetAt(ts: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(ts);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  const local = Date.UTC(
    get('year'),
    get('month') - 1,
    get('day'),
    get('hour'),
    get('minute'),
    get('second'),
  );
  return local - Math.floor(ts / 1000) * 1000;
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

// Местная полночь дня day (YYYY-MM-DD) в поясе timeZone — момент UTC (ISO). Смещение берётся на
// сам этот момент (переходы на летнее и зимнее время). Перевод часов вперёд ровно в полночь
// (Сантьяго, Гавана): полуночи нет — первый существующий момент дня (ревью M3), а не час прошлого дня.
export function dayStartUtc(day: string, timeZone: string, shiftDays = 0): string {
  const [y, m, d] = day.split('-').map(Number);
  const wall = Date.UTC(y ?? 0, (m ?? 1) - 1, (d ?? 1) + shiftDays);
  const local = (ts: number) => ts + offsetAt(ts, timeZone);
  let ts = wall - offsetAt(wall, timeZone);
  const again = offsetAt(ts, timeZone);
  if (again !== wall - ts) ts = wall - again;
  if (local(ts) !== wall) {
    // полуночи нет: двоичный поиск первой минуты, где местное время уже не раньше полуночи
    // (переводы часов — на границах минут; сдвиг не больше пары часов)
    let lo = ts - 3 * HOUR;
    let hi = ts + 3 * HOUR;
    while (hi - lo > MINUTE) {
      const mid = lo + Math.floor((hi - lo) / 2 / MINUTE) * MINUTE;
      if (local(mid) >= wall) hi = mid;
      else lo = mid;
    }
    ts = hi;
  }
  return new Date(ts).toISOString();
}

// Параметры listAudit: вкладка → action_prefix (действие важнее), период → UTC: «с» — полночь дня,
// «по» — полночь следующего дня (to у сервера не включается, день «по» — включительно).
export function toAuditQuery(s: AuditSearch, timeZone: string): AuditQuery {
  const q: AuditQuery = {};
  const prefix = s.action ?? (s.tab ? AUDIT_SECTIONS[s.tab] : undefined);
  if (prefix) q.action_prefix = prefix;
  if (s.actor) q.actor_staff_id = s.actor;
  if (s.object_type) q.object_type = s.object_type;
  if (s.object_id) q.object_id = s.object_id;
  if (s.from) q.from = dayStartUtc(s.from, timeZone);
  if (s.to) q.to = dayStartUtc(s.to, timeZone, 1);
  return q;
}
