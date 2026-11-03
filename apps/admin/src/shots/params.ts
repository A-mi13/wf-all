import type { Me } from '../app/permissions';
import { meGeoModerator } from '../test/geo';
import { meAdmin, meOwner, meSetup } from '../test/me';

// Параметры адреса режима shots (план UF1a.1, раунд 1 ревью: K-I1, m1): роль → me фикстуры тестов,
// язык → me.locale. Неизвестные роль и язык — ошибка (снимок не «молча на другом языке»); роль ищется
// только среди своих ключей (не toString и прочий прототип).
export const SHOTS_ROLES = {
  owner: meOwner,
  admin: meAdmin,
  moderator: meGeoModerator,
  setup: meSetup,
  anonymous: null,
} satisfies Record<string, Me | null>;

export type ShotsRole = keyof typeof SHOTS_ROLES;

const isRole = (v: string): v is ShotsRole => Object.hasOwn(SHOTS_ROLES, v);
const isLocale = (v: string): v is NonNullable<Me['locale']> => v === 'ru' || v === 'en';

export function shotsMe(params: URLSearchParams): { role: ShotsRole; me: Me | null } {
  const role = params.get('shots-role') ?? 'admin';
  if (!isRole(role)) throw new Error(`shots: неизвестная роль ${role}`);
  const locale = params.get('shots-locale');
  if (locale !== null && !isLocale(locale)) throw new Error(`shots: неизвестный язык ${locale}`);
  const base: Me | null = SHOTS_ROLES[role];
  return { role, me: base && locale ? { ...base, locale } : base };
}
