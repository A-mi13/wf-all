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
        'actions.submitForApproval',
        'roles.admin',
        'approvals.tabs.incoming',
        'approvals.approve',
      ]),
    );
  });

  it('стартовая и палитра (UF4.7): «Главная» и «Искать везде» входят в список', () => {
    expect(KEYS).toEqual(
      expect.arrayContaining(['nav.home', 'palette.open', 'palette.title', 'palette.input']),
    );
  });

  it('импорт и активация (план B, Task 23): подписи сценария geo.spec входят в список', () => {
    expect(KEYS).toEqual(
      expect.arrayContaining([
        'nav.imports',
        'geo.imports.start',
        'geo.imports.job.states.completed',
        'geo.imports.statuses.succeeded',
        'geo.imports.report.groups.conflict',
        'geo.activate.open',
        'geo.activate.places_label',
        'audit.actions.geo.import_requested',
        'audit.actions.geo.import',
        'audit.actions.geo.city_activated',
        'audit.actor.system',
      ]),
    );
  });

  it('t — текст ru по ключу', () => {
    expect(t('auth.login.submit')).toBe(ru.auth.login.submit);
    expect(t('roles.admin')).toBe(ru.roles.admin);
  });

  it('t — текст en по ключу (сценарий принятия на EN)', () => {
    expect(t('auth.accept.submit', 'en')).toBe(en.auth.accept.submit);
    expect(t('auth.setup.code', 'en')).toBe(en.auth.setup.code);
    expect(t('auth.accept.submit', 'en')).not.toBe(t('auth.accept.submit'));
  });

  it('t — ключ не из списка — ошибка (e2e не ищет по тексту мимо стража)', () => {
    // @ts-expect-error — ключа нет в KEYS
    expect(() => t('auth.login.nope')).toThrow(/KEYS/);
  });
});
