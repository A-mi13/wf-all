// @vitest-environment node
import { missingKeys } from '@wf/i18n';
import { describe, expect, it } from 'vitest';
import en from '../messages/en.json';
import ru from '../messages/ru.json';
import { KEYS, t } from './keys';

// Живой e2e находит поля, кнопки, вкладки и строки журнала по подписям из этих ключей:
// переименование ключа ломает этот быстрый тест в admin:test, а не живой прогон.
describe('ключи локалей e2e', () => {
  it('есть в ru и en (en — перевод, не пустой)', () => {
    expect(missingKeys(ru, [...KEYS])).toEqual([]);
    expect(missingKeys(en, [...KEYS])).toEqual([]);
  });

  it('контракт интерфейса Task 12a–15 (заметки для контроллера) входит в список', () => {
    expect(KEYS).toEqual(
      expect.arrayContaining([
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
        'nav.logout',
        'staff.invite.open',
        'staff.invite.email',
        'staff.invite.name',
        'staff.invite.role',
        'staff.invite.submit',
        'roles.admin',
        'approvals.tabs.incoming',
        'approvals.approve',
      ]),
    );
  });

  it('t — текст ru по ключу', () => {
    expect(t('auth.login.submit')).toBe(ru.auth.login.submit);
    expect(t('roles.admin')).toBe(ru.roles.admin);
  });

  it('t — ключ не из списка — ошибка (e2e не ищет по тексту мимо стража)', () => {
    // @ts-expect-error — ключа нет в KEYS
    expect(() => t('auth.login.nope')).toThrow(/KEYS/);
  });
});
