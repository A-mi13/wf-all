import { useSyncExternalStore } from 'react';
import { useIsCoarse } from '../media';

// Предпочтения таблиц (спека UI-фундамента §4.5, план U22): столбцы — localStorage wf.admin.table.<tableId>,
// плотность — wf.admin.density, одна на все таблицы. Свойство браузера, не секрет и не ПД. Каждый доступ к
// storage — в try/catch: приватный режим и переполнение бросают — тогда значения по умолчанию, а выбор живёт
// в памяти до перезагрузки.

export const TABLE_PREFS_PREFIX = 'wf.admin.table.';
export const DENSITY_STORAGE_KEY = 'wf.admin.density';

// hidden — скрытые пользователем; shown — явно включённые столбцы priority 'low' (ширина их не прячет).
export type ColumnPrefs = { hidden: string[]; shown: string[] };
export type Density = 'compact' | 'normal';

const EMPTY: ColumnPrefs = { hidden: [], shown: [] };

function getItem(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function setItem(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // приватный режим или переполнение — выбор не запомнится
  }
}

const strings = (v: unknown): string[] | null =>
  Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : null;

// Разбор записи без фильтра: мусор — null.
function parseColumns(raw: string | null): ColumnPrefs | null {
  if (raw === null) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null;
  const { hidden, shown } = parsed as Record<string, unknown>;
  const h = strings(hidden);
  const s = strings(shown);
  return h && s ? { hidden: h, shown: s } : null;
}

// Чтение: мусор — по умолчанию; id столбцов, которых нет в known (столбец убран в новой версии или ещё не
// пришёл — право, флаг), отбрасываются.
export function readColumns(tableId: string, known: readonly string[]): ColumnPrefs {
  const stored = parseColumns(getItem(TABLE_PREFS_PREFIX + tableId));
  if (!stored) return { ...EMPTY };
  const keep = (ids: string[]) => ids.filter((id) => known.includes(id));
  return { hidden: keep(stored.hidden), shown: keep(stored.shown) };
}

// Запись выбора по столбцам known: сохранённое о столбцах вне known (появится позже — право догрузилось) не
// стирается.
export function writeColumns(tableId: string, prefs: ColumnPrefs, known: readonly string[]) {
  const stored = parseColumns(getItem(TABLE_PREFS_PREFIX + tableId)) ?? EMPTY;
  const others = (ids: string[]) => ids.filter((id) => !known.includes(id));
  setItem(
    TABLE_PREFS_PREFIX + tableId,
    JSON.stringify({
      hidden: [...others(stored.hidden), ...prefs.hidden],
      shown: [...others(stored.shown), ...prefs.shown],
    }),
  );
}

// Плотность — общее хранилище для всех таблиц вкладки (useSyncExternalStore): смена в одной таблице видна
// всем; другая вкладка — событием storage. Storage недоступен — значение в памяти.
let memory: Density | null = null;
const listeners = new Set<() => void>();
const parseDensity = (v: string | null): Density => (v === 'compact' ? 'compact' : 'normal');

const density = {
  subscribe(onChange: () => void) {
    listeners.add(onChange);
    const onStorage = (e: StorageEvent) => {
      if (e.key !== null && e.key !== DENSITY_STORAGE_KEY) return;
      memory = null;
      onChange();
    };
    window.addEventListener('storage', onStorage);
    return () => {
      listeners.delete(onChange);
      window.removeEventListener('storage', onStorage);
    };
  },
  snapshot(): Density {
    return memory ?? parseDensity(getItem(DENSITY_STORAGE_KEY));
  },
  set(next: Density) {
    setItem(DENSITY_STORAGE_KEY, next);
    // память — только если storage не запомнил (иначе источник истины — storage: его меняет другая вкладка)
    memory = getItem(DENSITY_STORAGE_KEY) === next ? null : next;
    for (const fn of [...listeners]) fn();
  },
};
const serverDensity = (): Density => 'normal';

// [плотность, выбрать]: на грубом указателе (палец) — всегда normal, выбор хранится, но не применяется.
export function useDensity(): [Density, (d: Density) => void] {
  const chosen = useSyncExternalStore(density.subscribe, density.snapshot, serverDensity);
  const coarse = useIsCoarse();
  return [coarse ? 'normal' : chosen, density.set];
}
