// Недавнее палитры (спека UI-фундамента §4.1, план UF4.5): localStorage['wf.admin.recent.<staffId>'] — до 8 последних
// открытых из палитры сущностей и разделов, новое — первым. Запись — только { kind, id, label, path }: у сотрудника
// label — display_name, почты нет (лишние поля отбрасываются при записи). Общее устройство (m14): claimRecent(me.id) —
// при входе (SessionProvider) стирает недавнее других сотрудников, clearRecent() — при выходе (useLogout). Браузер
// закрыли без «Выйти» — имена коллег (без почт) лежат до следующего входа в этом браузере (принято спекой).
// Каждый доступ к storage — в try/catch: приватный режим и переполнение бросают — тогда недавнего нет.

export const RECENT_PREFIX = 'wf.admin.recent.';
export const RECENT_LIMIT = 8;

export type RecentKind = 'section' | 'city' | 'staff' | 'country';
export type RecentItem = { kind: RecentKind; id: string; label: string; path: string };

const KINDS: readonly string[] = ['section', 'city', 'staff', 'country'];

// Адрес — только путь этого приложения: не «//хост» и не схема.
const isAppPath = (p: string) => p.startsWith('/') && !p.startsWith('//');

function valid(v: unknown): RecentItem | null {
  if (typeof v !== 'object' || v === null) return null;
  const { kind, id, label, path } = v as Record<string, unknown>;
  if (typeof kind !== 'string' || !KINDS.includes(kind)) return null;
  if (typeof id !== 'string' || typeof label !== 'string' || typeof path !== 'string') return null;
  if (!isAppPath(path)) return null;
  return { kind: kind as RecentKind, id, label, path };
}

export function readRecent(staffId: string): RecentItem[] {
  let raw: string | null;
  try {
    raw = localStorage.getItem(RECENT_PREFIX + staffId);
  } catch {
    return [];
  }
  if (raw === null) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed
    .map(valid)
    .filter((x): x is RecentItem => x !== null)
    .slice(0, RECENT_LIMIT);
}

export function pushRecent(staffId: string, item: RecentItem) {
  const entry = valid(item);
  if (!entry) return;
  const next = [
    entry,
    ...readRecent(staffId).filter((i) => !(i.kind === entry.kind && i.id === entry.id)),
  ].slice(0, RECENT_LIMIT);
  try {
    localStorage.setItem(RECENT_PREFIX + staffId, JSON.stringify(next));
  } catch {
    // приватный режим или переполнение — недавнее не запомнится
  }
}

// Ключи недавнего, кроме keep.
function removeAll(keep?: string) {
  try {
    const keys = Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i));
    for (const k of keys) {
      if (k?.startsWith(RECENT_PREFIX) && k !== keep) localStorage.removeItem(k);
    }
  } catch {
    // storage недоступен — стирать нечего
  }
}

export function clearRecent() {
  removeAll();
}

export function claimRecent(staffId: string) {
  removeAll(RECENT_PREFIX + staffId);
}
