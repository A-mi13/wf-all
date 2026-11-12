// Возврат на ту же позицию (спека UI-фундамента §4.5, план UF2b.4). Прокрутку восстанавливает роутер
// (scrollRestoration: true, ключ — запись истории; контейнер таблицы — data-scroll-restoration-id), фокус
// на строке, с которой ушли, — таблица: строка запоминается по щелчку или Enter под ключом позиции и
// получает фокус, когда таблица с тем же ключом смонтирована, а фокус потерян (уход «Назад» — браузерной
// или общим возвратом каркаса). Память — только в памяти вкладки (id строк, не storage) и стирается при
// размонтировании каркаса (выход, другой сотрудник — key={me.id}, router.tsx).

// Ключи search открытой панели (карточка справа поверх списка): в ключ позиции не входят — панель
// открыли и закрыли, а список тот же. Наполняет UF2b.8 (панели в адресе).
export const PANEL_SEARCH_KEYS: readonly string[] = [];

// Ключ позиции: путь + search без ключей панели, порядок параметров не важен; фильтры (в том числе
// country) в ключе остаются.
export function restoreKeyOf(location: { pathname: string; search: object }): string {
  const entries = Object.entries(location.search)
    .filter(([k, v]) => v !== undefined && !PANEL_SEARCH_KEYS.includes(k))
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return entries.length > 0 ? `${location.pathname}?${JSON.stringify(entries)}` : location.pathname;
}

// Значение data-scroll-restoration-id: роутер пишет селектор контейнера в sessionStorage — текста адреса
// (поиск по почте, имени) там быть не должно, поэтому id — хеш ключа позиции (FNV-1a, 32 бита).
export function scrollRestorationId(restoreKey: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < restoreKey.length; i++) {
    h ^= restoreKey.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return `wf-table-${(h >>> 0).toString(36)}`;
}

const rows = new Map<string, string>();

export function rememberRow(restoreKey: string, rowKey: string): void {
  rows.set(restoreKey, rowKey);
}

export function rememberedRow(restoreKey: string): string | undefined {
  return rows.get(restoreKey);
}

export function forgetTablePositions(): void {
  rows.clear();
}
