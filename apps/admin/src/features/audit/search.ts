import type { operations } from '../../api/gen/admin';
import { parseKeys } from '../../app/search';
import { dayStartUtc } from '../../i18n/zone';

// Фильтры «Журнала действий» — типизированные search-параметры маршрута /audit (спека §9.1, §5.3):
// переживают перезагрузку и «Назад». Незнакомое и негодное значение из адреса отбрасывается — в
// запрос уходят только значения, которые сервер примет (uuid, код действия без шаблонов, дата).
// Период — календарные дни в местном времени сотрудника; в запрос — моменты UTC (правило 3).

// Вкладки по разделам → начало кода действия (`<модуль>.<действие>`, действия аудита бэкенда:
// staff.* — модуль сотрудников, platform.approval_* — решения подтверждений, geo.* — справочники
// (города, районы, страны, импорт GeoNames); platform.flag_* — флаги функций, platform.app_versions_* —
// версии приложения (план B, R15: действия флагов — platform.flag_changed, не flags.*)).
export const AUDIT_SECTIONS = {
  staff: 'staff.',
  approvals: 'platform.approval_',
  directories: 'geo.',
  flags: 'platform.flag_',
  versions: 'platform.app_versions_',
} as const;
export type AuditSection = keyof typeof AUDIT_SECTIONS;
export const SECTIONS: readonly AuditSection[] = [
  'staff',
  'approvals',
  'directories',
  'flags',
  'versions',
];

// Ключи фильтров пути (FILTER_SEARCH_KEYS, страж searchKeys.test.ts): парсер разбирает только их, по порядку
// (действие — после вкладки, «по» — после «с»).
export const KEYS = ['tab', 'action', 'actor', 'object_type', 'object_id', 'from', 'to'] as const;

export type AuditSearch = {
  tab?: AuditSection; // без вкладки — «Все»
  action?: string; // точный код действия — важнее вкладки
  actor?: string; // id сотрудника-автора
  object_type?: string;
  object_id?: string;
  from?: string; // YYYY-MM-DD, включительно
  to?: string; // YYYY-MM-DD, включительно
};

export type AuditQuery = NonNullable<operations['listAudit']['parameters']['query']>;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// Точный код действия: минимум два сегмента через точку, без пустых, не на «.» и «_» — такое значение
// сервер понял бы как начало строки (action_prefix, контракт 305026c), а односегментное («staff») — как
// весь модуль с подвидами; фильтр «Действие» — только точный код (ревью уборки №7 m3, уборка №8 m2).
const ACTION = /^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$/;
const isAction = (v: string) => v.length <= 100 && ACTION.test(v) && !/[._]$/.test(v);
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
  return parseKeys<(typeof KEYS)[number], AuditSearch>(
    KEYS,
    {
      tab: (v) => (isSection(v) ? v : undefined),
      // действие из другого раздела, чем вкладка, — не берём: вкладка видна, а искал бы чужое
      action: (v, out) => {
        const action = str(v, isAction);
        return action && (!out.tab || action.startsWith(AUDIT_SECTIONS[out.tab]))
          ? action
          : undefined;
      },
      actor: (v) => str(v, isUuid),
      object_type: (v) => str(v, (x) => OBJECT_TYPE.test(x)),
      object_id: (v) => str(v, isUuid),
      from: (v) => str(v, isDay),
      // «по» раньше «с» — сервер ответил бы 400 (range): такой «по» не берём
      to: (v, out) => {
        const to = str(v, isDay);
        return to && !(out.from && to < out.from) ? to : undefined;
      },
    },
    s,
  );
}

// Местная полночь дня в поясе — src/i18n/zone.ts (прямой Intl в экранах запрещён lint-правилом, план UF2a.9).
export { dayStartUtc };

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
