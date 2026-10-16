// «Кому и что» запроса на подтверждение — из payload (поля действий staff.*: приглашение — почта,
// имя, роль, города; смена роли — было и стало; сброс и выключение владельца — роль, причина,
// комментарий). payload в контракте — открытый объект: берутся только знакомые поля нужного типа,
// служебные (v, init_id, init_ver, version, target_id) и незнакомые не показываются. Почта — как
// хранится (IDN-домен в A-форме xn--…, не декодируется: её же вводят в подтверждениях).
export type ApprovalSummary = {
  name?: string;
  email?: string;
  fromRole?: string;
  role?: string;
  fromCityIds?: string[];
  cityIds?: string[];
  reason?: string;
  comment?: string;
};

const text = (v: unknown): string | undefined =>
  typeof v === 'string' && v.trim() !== '' ? v : undefined;
const ids = (v: unknown): string[] | undefined =>
  Array.isArray(v) && v.every((x) => typeof x === 'string') ? v : undefined;

export function summarizePayload(payload: Record<string, unknown>): ApprovalSummary {
  const out: ApprovalSummary = {};
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
