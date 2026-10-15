import type { components } from '../../api/gen/admin';
import { can, type Decision, decide, type Me } from '../../app/permissions';

// Правила кнопок «Ролей и доступа» (спека §2.5–2.6, §9.3). Право — только из me.permissions; роль
// цели решает, КАКОЕ право спрашивать. Интерфейс прячет заведомо запрещённое и показывает dual как
// «отправить на подтверждение»; окончательно решает сервер (200/202/403/409).

export type StaffRole = components['schemas']['StaffRole'];
export type StaffAccount = components['schemas']['StaffAccount'];
type StaffRoles = components['schemas']['StaffRoles'];

// Роли по старшинству. Список ролей в контракте открытый: незнакомая роль цели — без действий
// (снятие, сброс) или как elevated (смена роли) — строже, чем угадывать.
export const ROLES: readonly StaffRole[] = ['owner', 'admin', 'moderator', 'support'];

export const isKnownRole = (r: string): r is StaffRole => (ROLES as readonly string[]).includes(r);
const isElevated = (r: string) => !isKnownRole(r) || r === 'owner' || r === 'admin';

// Выдача роли: владельца и админа — grant_elevated, модератора и поддержку — grant.
const grantPermission = (role: string) =>
  isElevated(role) ? 'staff.roles.grant_elevated' : 'staff.roles.grant';

// Роли, которые сотрудник может выдать приглашением (§9.3: кнопка «Пригласить» и выбор роли).
export function grantableRoles(me: Me): StaffRole[] {
  return ROLES.filter((r) => can(me, grantPermission(r)));
}

type RoleState = { role: string; city_ids: readonly string[] };

// Право изменения роли и городов (§2.6); null — изменения нет:
// - старая или новая роль owner/admin — grant_elevated (вдвоём);
// - роль меняется или добавлен хотя бы один город — grant (вдвоём);
// - только снятие городов — revoke (сразу).
export function roleChangePermission(from: RoleState, to: RoleState): string | null {
  const before = new Set(from.city_ids);
  const added = to.city_ids.some((c) => !before.has(c));
  const removed = from.city_ids.some((c) => !to.city_ids.includes(c));
  if (from.role === to.role && !added && !removed) return null;
  if (isElevated(from.role) || isElevated(to.role)) return 'staff.roles.grant_elevated';
  if (from.role !== to.role || added) return 'staff.roles.grant';
  return 'staff.roles.revoke';
}

// Выключение по роли цели: владелец — revoke_owner (вдвоём), админ — revoke_admin, остальные — revoke.
function disablePermission(role: string): string | null {
  if (!isKnownRole(role)) return null;
  if (role === 'owner') return 'staff.roles.revoke_owner';
  if (role === 'admin') return 'staff.roles.revoke_admin';
  return 'staff.roles.revoke';
}

// Сброс доступа по роли цели: владелец и админ — reset_elevated, остальные — reset (всегда вдвоём).
function resetPermission(role: string): string | null {
  if (!isKnownRole(role)) return null;
  return isElevated(role) ? 'staff.access.reset_elevated' : 'staff.access.reset';
}

export type StaffActions = {
  self: boolean; // своя строка: «это вы», действий нет (staff.self_action)
  resend: boolean; // переотправить приглашение
  // Роли в форме смены роли; пусто — формы нет. Текущая роль цели — первой не обязательно.
  roleOptions: StaffRole[];
  sessions: boolean; // сеансы цели и «Завершить все»
  reset: Decision;
  disable: Decision;
};

const NONE: Omit<StaffActions, 'self'> = {
  resend: false,
  roleOptions: [],
  sessions: false,
  reset: 'deny',
  disable: 'deny',
};

export function staffActions(me: Me, target: StaffAccount): StaffActions {
  if (target.id === me.id) return { self: true, ...NONE };
  const live = target.status !== 'disabled';
  const ask = (permission: string | null): Decision =>
    live && permission ? decide(me, permission) : 'deny';

  // Смена роли: роль r доступна, если право на переход target.role → r есть; текущая роль —
  // если её города можно менять (модератор: добавить — grant, убрать — revoke).
  const other = ROLES.filter(
    (r) =>
      r !== target.role && ask(roleChangePermission(target, { role: r, city_ids: [] })) !== 'deny',
  );
  const citiesEditable =
    target.role === 'moderator' &&
    (ask('staff.roles.grant') !== 'deny' || ask('staff.roles.revoke') !== 'deny');
  const roleOptions =
    other.length > 0 || citiesEditable
      ? ROLES.filter((r) => r === target.role || other.includes(r))
      : [];

  // Сеансы чужих владельцев и админов — только тому, кто сам может выдавать эти роли (админ их
  // не видит, §2.5): по праву, а не по me.role.
  const sessions =
    live &&
    can(me, 'staff.sessions.revoke') &&
    (!isElevated(target.role) || can(me, 'staff.roles.grant_elevated'));

  return {
    self: false,
    resend: target.status === 'invited' && can(me, 'staff.invites.resend'),
    roleOptions,
    sessions,
    reset: ask(resetPermission(target.role)),
    disable: ask(disablePermission(target.role)),
  };
}

export const hasActions = (a: StaffActions): boolean =>
  !a.self &&
  (a.resend ||
    a.roleOptions.length > 0 ||
    a.sessions ||
    a.reset !== 'deny' ||
    a.disable !== 'deny');

// «Правило двух рук» (§9.4 п. 5) — по матрице: права, у которых хотя бы одна роль только вдвоём, и
// эти роли. Текст «права меняет только владелец» не пишется руками — он следует из данных.
export function twoHandsRules(m: StaffRoles): { key: string; roles: string[] }[] {
  return m.permissions
    .map((p) => ({
      key: p.key,
      roles: m.roles.filter((r) => p.grants[r] === 'dual'),
    }))
    .filter((p) => p.roles.length > 0);
}
