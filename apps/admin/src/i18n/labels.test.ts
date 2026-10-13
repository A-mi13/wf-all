import { loadLabelCatalog } from '@wf/contracts/labels';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';

// D21 (спека сотрудников §9.5): подписи прав и действий подтверждений — только в локалях, ru и en
// (en — перевод: в админке нет отката на ru). Новое право в каталоге или новое действие в
// контракте без текста роняет этот тест.
const catalog = await loadLabelCatalog();
const required = [
  ...catalog.permissions.map((k) => `permissions.${k}`),
  ...catalog.approvalActions.map((a) => `approvals.actions.${a}`),
];

test('каталог подписей не пуст', () => {
  expect(catalog.permissions).toContain('staff.roles.grant');
  expect(catalog.approvalActions).toContain('staff.invite');
});

test('у каждого права и действия подтверждения есть подпись ru', () => {
  expect(missingKeys(ru, required)).toEqual([]);
});

test('у каждого права и действия подтверждения есть подпись en', () => {
  expect(missingKeys(en, required)).toEqual([]);
});
