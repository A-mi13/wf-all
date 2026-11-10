// Термины глоссария (спека UI-фундамента §4.8): тексты подсказок TermHint — messages glossary.<ключ>,
// ru и en (страж — src/i18n/labels.test.ts).
export const GLOSSARY_KEYS = [
  'dual_approval', // подтверждение вдвоём
  'scoped', // доступ в своих городах
  'city_status', // ожидание / пилот / живой
  'geonames',
  'slug', // адрес в вебе
  'recovery_codes',
  'feature_flag',
] as const;
export type GlossaryKey = (typeof GLOSSARY_KEYS)[number];
