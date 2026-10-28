import { loadLabelCatalog } from '@wf/contracts/labels';
import { missingKeys } from '@wf/i18n';
import { expect, test } from 'vitest';
import en from '../../messages/en.json';
import ru from '../../messages/ru.json';
import { NAV } from '../app/nav';
import { SECTIONS } from '../features/audit/search';

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

// План B, Task 17: каталог видит действия geo и флаги (без этого страж выше был бы вакуумным для них);
// меню «Справочников», вкладки и теги журнала, подписи действий и объектов аудита плана B (R15,
// Приложение 4), статусы городов и «включена/выключена» страны — в ru и en.
test('каталог знает действия и права плана B', () => {
  expect(catalog.approvalActions).toEqual(
    expect.arrayContaining(['geo.country_rules', 'geo.country_create']),
  );
  expect(catalog.permissions).toContain('geo.countries.rules');
  expect(catalog.flags).toContain('gk_rotation');
});

const planB = [
  ...NAV.flatMap((g) => [g.labelKey, ...g.items.map((i) => i.labelKey)]),
  ...SECTIONS.flatMap((s) => [`audit.tabs.${s}`, `audit.tags.${s}`]),
  ...[
    'city_activated',
    'city_updated',
    'city_linked',
    'district_created',
    'district_updated',
    'country_created',
    'country_updated',
    'import_requested',
    'import_cancelled',
  ].map((a) => `audit.actions.geo.${a}`),
  'audit.actions.platform.flag_changed',
  'audit.actions.platform.app_versions_changed',
  ...['country', 'district', 'feature_flag', 'app_versions', 'geonames_import_job'].map(
    (o) => `audit.objects.${o}`,
  ),
  ...['waitlist', 'pilot', 'live'].map((s) => `geo.status.${s}`),
  'geo.countries.enabled',
  'geo.countries.disabled',
];

test('подписи «Справочников» и журнала плана B — в ru и en', () => {
  expect(missingKeys(ru, planB)).toEqual([]);
  expect(missingKeys(en, planB)).toEqual([]);
});
