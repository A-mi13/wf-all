import { loadLabelCatalog } from '@wf/contracts/labels';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

// D21 (спека сотрудников §9.5): подписи прав, действий подтверждений и флагов функций — только в
// локалях, ru и en (en — перевод: в админке нет отката на ru). Новое право в каталоге, новое действие
// или новый ключ флага (enum FeatureFlagKey, R6) в контракте без текста роняет этот тест.
const catalog = await loadLabelCatalog();
const required = [
  ...catalog.permissions.map((k) => `permissions.${k}`),
  ...catalog.approvalActions.map((a) => `approvals.actions.${a}`),
  ...catalog.flags.flatMap((k) => [`flags.${k}.title`, `flags.${k}.note`]),
];

test('каталог подписей не пуст', () => {
  expect(catalog.permissions).toContain('staff.roles.grant');
  expect(catalog.approvalActions).toContain('staff.invite');
  expect(catalog.flags).toContain('subscriptions');
});

test('у каждого права, действия подтверждения и флага есть подпись ru', () => {
  expect(missingKeys(ru, required)).toEqual([]);
});

test('у каждого права, действия подтверждения и флага есть подпись en', () => {
  expect(missingKeys(en, required)).toEqual([]);
});
