// «Кому и что» запроса на подтверждение — из payload (поля действий staff.*: приглашение — почта,
// имя, роль, города; смена роли — было и стало; сброс и выключение владельца — роль, причина,
// комментарий). payload в контракте — открытый объект: берутся только знакомые поля нужного типа,
// служебные (v, init_id, init_ver, version, target_id) и незнакомые не показываются. Почта — как
// хранится (IDN-домен в A-форме xn--…, не декодируется: её же вводят в подтверждениях).
// Действия geo (план B, Task 13 — формат payload): geo.country_rules — код страны и «было → стало»
// из from/to (валюта, возрастные пороги, включение); geo.country_create — правила новой страны.
// Поля geo читаются только у своего действия.
export type CountryChange =
  | { field: 'currency'; from: string; to: string }
  | { field: 'min_signup_age' | 'age_of_majority'; from: number; to: number }
  | { field: 'is_enabled'; from: boolean; to: boolean };

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
  country?: { code: string; changes: CountryChange[] };
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

// Поля «было → стало» правил страны — в этом порядке.
function countryChanges(from: Record<string, unknown>, to: Record<string, unknown>) {
  const out: CountryChange[] = [];
  const [cf, ct] = [text(from.currency), text(to.currency)];
  if (cf && ct) out.push({ field: 'currency', from: cf, to: ct });
  for (const field of ['min_signup_age', 'age_of_majority'] as const) {
    const [f, t] = [int(from[field]), int(to[field])];
    if (f !== undefined && t !== undefined) out.push({ field, from: f, to: t });
  }
  const [ef, et] = [bool(from.is_enabled), bool(to.is_enabled)];
  if (ef !== undefined && et !== undefined) out.push({ field: 'is_enabled', from: ef, to: et });
  return out;
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
      out.country = { code, changes: countryChanges(record(payload.from), record(payload.to)) };
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
