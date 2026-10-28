// «Кому и что» запроса на подтверждение — из payload (поля действий staff.*: приглашение — почта,
// имя, роль, города; смена роли — было и стало; сброс и выключение владельца — роль, причина,
// комментарий). payload в контракте — открытый объект: берутся только знакомые поля нужного типа,
// служебные (v, init_id, init_ver, version, target_id) и незнакомые не показываются. Почта — как
// хранится (IDN-домен в A-форме xn--…, не декодируется: её же вводят в подтверждениях).
// Действия geo (план B, Task 13 — формат payload): geo.country_rules — код страны и «было → стало»
// из from/to; geo.country_create — правила новой страны. Поля geo читаются только у своего действия.
// У правил страны подтверждающий видит ВСЁ, что исполнится (ревью G8, I1): запрос несёт весь patch,
// включая поля «сразу», а аудит запишет автором подтверждающего. Знакомые поля — «было → стало»,
// незнакомые ключи и значения неверного типа — списком others («и ещё N полей»), не молча мимо.
export type CountryChange =
  | { field: 'currency' | 'phone_prefix'; from: string; to: string }
  | { field: 'week_starts_on' | 'min_signup_age' | 'age_of_majority'; from: number; to: number }
  | { field: 'is_enabled'; from: boolean; to: boolean }
  | { field: 'names'; locale: string; from: string | null; to: string | null };

export type NewCountry = {
  code: string;
  currency?: string;
  defaultLocale?: string;
  phonePrefix?: string;
  weekStartsOn?: number;
  minSignupAge?: number;
  ageOfMajority?: number;
  isEnabled?: boolean;
};

export type ApprovalSummary = {
  name?: string;
  email?: string;
  fromRole?: string;
  role?: string;
  fromCityIds?: string[];
  cityIds?: string[];
  reason?: string;
  comment?: string;
  country?: { code: string; changes: CountryChange[]; others: string[] };
  newCountry?: NewCountry;
};

const text = (v: unknown): string | undefined =>
  typeof v === 'string' && v.trim() !== '' ? v : undefined;
const ids = (v: unknown): string[] | undefined =>
  Array.isArray(v) && v.every((x) => typeof x === 'string') ? v : undefined;
const int = (v: unknown): number | undefined => (Number.isInteger(v) ? Number(v) : undefined);
const bool = (v: unknown): boolean | undefined => (typeof v === 'boolean' ? v : undefined);
const record = (v: unknown): Record<string, unknown> =>
  v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.entries(v)) : {};

// Поля «было → стало» правил страны — в этом порядке; ключи from/to вне списка или с неверным типом —
// в others (по алфавиту).
const STRING_FIELDS = ['currency', 'phone_prefix'] as const;
const INT_FIELDS = ['week_starts_on', 'min_signup_age', 'age_of_majority'] as const;
const KNOWN = [
  'currency',
  'phone_prefix',
  'week_starts_on',
  'min_signup_age',
  'age_of_majority',
  'is_enabled',
  'names',
];

const nameValue = (v: unknown): string | null | undefined =>
  v === null || v === undefined ? null : typeof v === 'string' ? v : undefined;

// Названия по языкам: из from/to — объекты { язык: строка | null }; отсутствие и null — «нет».
function nameChanges(from: unknown, to: unknown): CountryChange[] | undefined {
  const isNames = (v: unknown) =>
    v === undefined ||
    (v !== null &&
      typeof v === 'object' &&
      !Array.isArray(v) &&
      Object.values(v).every((x) => nameValue(x) !== undefined));
  if (!isNames(from) || !isNames(to)) return undefined;
  const [f, t] = [record(from), record(to)];
  const locales = [...new Set([...Object.keys(f), ...Object.keys(t)])].sort();
  return locales.map((locale) => ({
    field: 'names' as const,
    locale,
    from: nameValue(f[locale]) ?? null,
    to: nameValue(t[locale]) ?? null,
  }));
}

function countryChanges(from: Record<string, unknown>, to: Record<string, unknown>) {
  const changes: CountryChange[] = [];
  const bad = new Set<string>();
  const keys = new Set([...Object.keys(from), ...Object.keys(to)]);
  for (const field of STRING_FIELDS) {
    if (!keys.has(field)) continue;
    const [f, t] = [text(from[field]), text(to[field])];
    if (f && t) changes.push({ field, from: f, to: t });
    else bad.add(field);
  }
  for (const field of INT_FIELDS) {
    if (!keys.has(field)) continue;
    const [f, t] = [int(from[field]), int(to[field])];
    const day = (n: number | undefined) =>
      field !== 'week_starts_on' || (n !== undefined && n >= 1 && n <= 7);
    if (f !== undefined && t !== undefined && day(f) && day(t))
      changes.push({ field, from: f, to: t });
    else bad.add(field);
  }
  // неделя — до порогов (порядок полей формы страны)
  changes.sort((a, b) => KNOWN.indexOf(a.field) - KNOWN.indexOf(b.field));
  if (keys.has('is_enabled')) {
    const [f, t] = [bool(from.is_enabled), bool(to.is_enabled)];
    if (f !== undefined && t !== undefined) changes.push({ field: 'is_enabled', from: f, to: t });
    else bad.add('is_enabled');
  }
  if (keys.has('names')) {
    const names = nameChanges(from.names, to.names);
    if (names) changes.push(...names);
    else bad.add('names');
  }
  const others = [...keys].filter((k) => !KNOWN.includes(k) || bad.has(k)).sort();
  return { changes, others };
}

function newCountry(p: Record<string, unknown>, code: string): NewCountry {
  const out: NewCountry = { code };
  const set = <K extends keyof NewCountry>(k: K, v: NewCountry[K] | undefined) => {
    if (v !== undefined) out[k] = v;
  };
  set('currency', text(p.currency));
  set('defaultLocale', text(p.default_locale));
  set('phonePrefix', text(p.phone_prefix));
  const week = int(p.week_starts_on);
  set('weekStartsOn', week !== undefined && week >= 1 && week <= 7 ? week : undefined);
  set('minSignupAge', int(p.min_signup_age));
  set('ageOfMajority', int(p.age_of_majority));
  set('isEnabled', bool(p.is_enabled));
  return out;
}

export function summarizePayload(
  payload: Record<string, unknown>,
  action?: string,
): ApprovalSummary {
  const out: ApprovalSummary = {};
  if (action === 'geo.country_rules' || action === 'geo.country_create') {
    const code = text(payload.code);
    if (!code) return out;
    if (action === 'geo.country_rules')
      out.country = { code, ...countryChanges(record(payload.from), record(payload.to)) };
    else out.newCountry = newCountry(payload, code);
    return out;
  }
  const name = text(payload.display_name);
  if (name) out.name = name;
  const email = text(payload.email);
  if (email) out.email = email;
  const fromRole = text(payload.from_role);
  if (fromRole) out.fromRole = fromRole;
  const role = text(payload.role);
  if (role) out.role = role;
  const fromCityIds = ids(payload.from_city_ids);
  if (fromCityIds) out.fromCityIds = fromCityIds;
  const cityIds = ids(payload.city_ids);
  if (cityIds) out.cityIds = cityIds;
  const reason = text(payload.reason);
  if (reason) out.reason = reason;
  const comment = text(payload.comment);
  if (comment) out.comment = comment;
  return out;
}
