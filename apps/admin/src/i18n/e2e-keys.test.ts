import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

// Живой e2e (Task 16) находит поля экранов входа и формы приглашения по подписям из этих ключей:
// имена фиксированы, переименование ломает e2e молча — страж здесь.
const E2E_KEYS = [
  'auth.login.email',
  'auth.login.password',
  'auth.login.submit',
  'auth.mfa.code',
  'auth.accept.email',
  'auth.accept.password',
  'auth.accept.passwordRepeat',
  'auth.accept.code',
  'auth.accept.submit',
  'auth.setup.code',
  'auth.setup.saved',
  'auth.setup.finish',
  // «Роли и доступ» → «Пригласить» (Task 14)
  'staff.invite.open',
  'staff.invite.email',
  'staff.invite.name',
  'staff.invite.role',
  'staff.invite.submit',
  // «Подтверждения» → «Входящие» → «Подтвердить» (Task 15)
  'approvals.tabs.incoming',
  'approvals.approve',
];

test('ключи подписей для e2e есть в ru и en (en — перевод, не пустой)', () => {
  expect(missingKeys(ru, E2E_KEYS)).toEqual([]);
  expect(missingKeys(en, E2E_KEYS)).toEqual([]);
});
