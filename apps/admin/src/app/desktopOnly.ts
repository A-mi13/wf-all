// «Только с компьютера» (спека UI-фундамента §3.2; спека сотрудников §9.4 «Телефон»): действия, которые
// выдают или снимают права сотрудника, на карманном устройстве (useIsHandheld — палец и экран уже
// compact) заменены пометкой. Ограничение интерфейса, не защита: решает всегда сервер. Разделы открыты
// все; определяет устройство, а не ширина окна. В ActionGate передаётся право САМОГО действия:
// «Завершить все сеансы» цели — staff.sessions.revoke (не в реестре), даже если блок сеансов виден
// только при grant_elevated (m5).
export const DESKTOP_ONLY_PERMISSIONS: ReadonlySet<string> = new Set([
  'staff.roles.grant',
  'staff.roles.grant_elevated',
  'staff.roles.revoke',
  'staff.roles.revoke_admin',
  'staff.roles.revoke_owner',
  'staff.access.reset',
  'staff.access.reset_elevated',
]);

// Хоть одно право в реестре — действие только с компьютера.
export function isDesktopOnly(permission: string | readonly string[]): boolean {
  const list: readonly string[] = typeof permission === 'string' ? [permission] : permission;
  return list.some((p) => DESKTOP_ONLY_PERMISSIONS.has(p));
}

// Решения подтверждений: любое действие staff.*, включая незнакомое будущее, — только с компьютера
// (закрыто по умолчанию; таблица «действие → право» пропустила бы новое действие — m4).
export function isDesktopOnlyDecision(action: string): boolean {
  return action.startsWith('staff.');
}
