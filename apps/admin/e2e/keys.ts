import { readFileSync } from 'node:fs';

// Ключи локалей, по подписям которых живой e2e находит элементы (контракт интерфейса Task 12a–15,
// «Заметки для контроллера» плана A). Одно место: переименовали ключ в messages — правится здесь, а
// страж keys.test.ts (admin:test) ловит расхождение до живого прогона.
export const KEYS = [
  // вход: шаг 1 и второй фактор
  'auth.login.email',
  'auth.login.password',
  'auth.login.submit',
  'auth.mfa.code',
  // принятие приглашения
  'auth.accept.email',
  'auth.accept.password',
  'auth.accept.passwordRepeat',
  'auth.accept.code',
  'auth.accept.submit',
  // настройка 2FA
  'auth.setup.code',
  'auth.setup.codes_title',
  'auth.setup.saved',
  'auth.setup.finish',
  // каркас
  'nav.logout',
  'nav.audit',
  'nav.approvals',
  // «Роли и доступ» → «Пригласить»
  'staff.invite.open',
  'staff.invite.email',
  'staff.invite.name',
  'staff.invite.role',
  'staff.invite.submit',
  'roles.owner',
  'roles.admin',
  'roles.support',
  // «Подтверждения»
  'approvals.tabs.incoming',
  'approvals.approve',
  'approvals.reject',
  'approvals.reject_dialog.title',
  'approvals.reject_dialog.comment',
  'approvals.reject_dialog.submit',
  'approvals.done.executed',
  // «Журнал действий»
  'audit.actor.operator',
  'audit.actions.staff.bootstrapped',
  'audit.actions.staff.invite_accepted',
  'audit.actions.platform.approval_requested',
  'audit.actions.platform.approval_executed',
] as const;

export type Key = (typeof KEYS)[number];

const ru: unknown = JSON.parse(
  readFileSync(new URL('../messages/ru.json', import.meta.url), 'utf8'),
);

// Текст ru по ключу (e2e идёт на ru: язык по умолчанию, у новых сотрудников — тоже). Ключ не из
// KEYS — ошибка: поиск мимо стража сломался бы молча при переименовании.
export function t(key: Key): string {
  if (!(KEYS as readonly string[]).includes(key)) {
    throw new Error(`e2e: ключа «${key}» нет в KEYS (e2e/keys.ts)`);
  }
  const v = key.split('.').reduce<unknown>((node, part) => {
    return node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined;
  }, ru);
  if (typeof v !== 'string' || v === '') throw new Error(`e2e: нет текста ru для «${key}»`);
  return v;
}
