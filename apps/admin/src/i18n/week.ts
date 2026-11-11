// Первый день недели календаря — по языку интерфейса (спека UI-фундамента §4.17): Intl.Locale#getWeekInfo
// (или свойство weekInfo в части движков); нет API — запасная таблица наших языков по тем же данным CLDR.
// Правило страны (week_start справочника) к интерфейсу админки не относится. 1 — понедельник, 7 — воскресенье.
type WeekInfo = { firstDay?: number };
type LocaleWithWeek = Intl.Locale & { getWeekInfo?: () => WeekInfo; weekInfo?: WeekInfo };

const FALLBACK: Record<string, 1 | 7> = { ru: 1, en: 7 };

export function firstDayOfWeek(locale: string): 1 | 7 {
  try {
    const loc = new Intl.Locale(locale) as LocaleWithWeek;
    const info = typeof loc.getWeekInfo === 'function' ? loc.getWeekInfo() : loc.weekInfo;
    if (info?.firstDay === 7) return 7;
    if (info?.firstDay !== undefined) return 1;
  } catch {
    // негодный тег языка — таблица
  }
  return FALLBACK[locale.split('-')[0] ?? ''] ?? 1;
}
