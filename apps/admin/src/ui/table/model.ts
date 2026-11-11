import type { InfiniteData, UseInfiniteQueryResult } from '@tanstack/react-query';
import type { ReactNode } from 'react';

// Типы и помощники таблицы (DataTable.tsx) — отдельно от компонента (react-refresh).

export type Column<T> = {
  id: string;
  header: string; // подпись столбца — текст из локалей экрана
  cell: (row: T) => ReactNode;
  sortable?: boolean;
  align?: 'start' | 'end'; // числа и даты — end
  width?: 'sm' | 'md' | 'lg'; // без ширины столбец тянется
  // Меню «Столбцы» (спека UI-фундамента §4.5): по умолчанию скрывается любой, кроме первого.
  hideable?: boolean;
  // 'low' — второстепенный: на 768–1023 (по окну) скрыт, пока не включён в «Столбцах» явно; в карточке
  // телефона — под «Подробнее». От 1024 видны все.
  priority?: 'high' | 'low';
};

export type SortState = { column: string; direction: 'asc' | 'desc' };
export type ChipOption = { value: string; label: string };

export type DataTableProps<T> = {
  label: string; // имя таблицы для читалки экрана
  columns: Column<T>[];
  rowKey: (row: T) => string;
  // Данные: rows === undefined и нет error — первая загрузка (скелетон).
  rows: T[] | undefined;
  error?: unknown; // ошибка первой загрузки: строк нет
  onRetry: () => void;
  retrying?: boolean; // перезапрос после ошибки идёт — «Повторить» занята
  hasMore?: boolean;
  loadingMore?: boolean;
  loadMoreError?: unknown; // «Показать ещё» не удалось — строки остаются
  onLoadMore?: () => void;
  // Фильтры
  search?: { value: string; onChange: (value: string) => void; placeholder?: string };
  chips?: { label: string; options: ChipOption[]; value: string; onChange: (v: string) => void };
  filters?: { content: ReactNode; active: number }; // раскрывающийся фильтр; active — сколько задано
  dirty?: boolean; // фильтры отличаются от исходных — показать «Сбросить фильтры»
  onReset?: () => void;
  sort?: SortState;
  onSortChange?: (sort: SortState) => void;
  // Пусто без фильтров — своё состояние экрана («Очередь пуста»); по умолчанию «Ничего не нашлось».
  empty?: ReactNode;
  // «Показано N» под панелью; false — статичная таблица (матрица прав), где число строк не нужно.
  summary?: boolean;
  // Раскладка строк: 'auto' (по умолчанию) — на узком экране (useIsMobile) строки-карточки; 'table' —
  // таблица на любой ширине со своей горизонтальной прокруткой (матрица прав: карточки ей вредят —
  // спека UI-фундамента §3.1, §3.5).
  layout?: 'auto' | 'table';
  // Карточки вместо строк (сетка, макет «Города и запуск»): та же панель, сводка, «Показать ещё» и
  // состояния; данные — список с именем label, columns не используются.
  card?: (row: T) => ReactNode;
  // Список выбора вместо строк (шаг «найти место» в панели): role="listbox", строка — option с
  // выделением, клик и стрелки выбирают, Enter и двойной клик — главное действие (onActivate).
  // Прокручивается только сам список (родитель задаёт высоту), действие — в подвале панели у экрана.
  select?: {
    value: string | null; // rowKey выбранной строки
    onChange: (row: T) => void;
    onActivate?: (row: T) => void;
    option: (row: T) => ReactNode;
  };
  onRowClick?: (row: T) => void;
  activeRowKey?: string; // строка, открытая в панели справа
  // Закрепление (спека UI-фундамента §4.5): шапка липнет внутри своей области прокрутки (по умолчанию — при
  // строках > STICKY_HEADER_FROM), первый столбец — у широких таблиц (матрица прав, и при layout 'table' на
  // телефоне). Ширина закреплённого столбца — его width (по умолчанию 'lg').
  stickyHeader?: boolean;
  stickyFirstColumn?: boolean;
  // Меню «Столбцы» и плотность: без tableId кнопки нет; выбор — localStorage wf.admin.table.<tableId>.
  tableId?: string;
};

// Шапка закрепляется по умолчанию, когда строк больше этого.
export const STICKY_HEADER_FROM = 20;

// Видимость столбцов: выбор пользователя важнее ширины. Скрытый в «Столбцах» (если скрываемый) не виден;
// 'low' без явного включения (shown) скрыт по ширине окна (narrow — 768–1023); единственный оставшийся
// ширина не прячет — таблица без столбцов была бы тупиком.
export function columnVisibility<T>(
  columns: Column<T>[],
  prefs: { hidden: readonly string[]; shown: readonly string[] },
  narrow: boolean,
): { visible: Column<T>[]; byWidth: Set<string> } {
  const picked = columns.filter((c, i) => !(isHideable(c, i) && prefs.hidden.includes(c.id)));
  // storage скрыл все (правили руками, столбцы стали скрываемыми) — показать все
  const chosen = picked.length > 0 ? picked : columns;
  const byWidth = new Set(
    narrow
      ? chosen.filter((c) => c.priority === 'low' && !prefs.shown.includes(c.id)).map((c) => c.id)
      : [],
  );
  const visible = chosen.filter((c) => !byWidth.has(c.id));
  if (visible.length === 0) return { visible: chosen, byWidth: new Set() };
  return { visible, byWidth };
}

// Скрываемый: явно заданное hideable, иначе — любой, кроме первого.
export const isHideable = <T>(c: Column<T>, index: number) => c.hideable ?? index > 0;

// Пауза набора в поиске, после которой уходит запрос.
export const SEARCH_DEBOUNCE_MS = 300;

type CursorPage<T> = { items: T[]; next_cursor: string | null };

// useInfiniteQuery со страницами {items, next_cursor} → пропсы данных таблицы.
// getNextPageParam запроса: (page) => page.next_cursor ?? undefined.
export function infiniteTableProps<T, P, E>(
  q: UseInfiniteQueryResult<InfiniteData<CursorPage<T>, P>, E>,
): Pick<
  DataTableProps<T>,
  | 'rows'
  | 'error'
  | 'onRetry'
  | 'retrying'
  | 'hasMore'
  | 'loadingMore'
  | 'loadMoreError'
  | 'onLoadMore'
> {
  return {
    rows: q.data?.pages.flatMap((p) => p.items),
    error: q.data ? undefined : (q.error ?? undefined),
    onRetry: () => void q.refetch(),
    // У TanStack Query перезапрос без данных сбрасывает ошибку (status pending) — таблица сразу
    // показывает скелетон; retrying держит «Повторить» занятой, если ошибка остаётся на экране.
    retrying: q.isFetching,
    hasMore: q.hasNextPage,
    loadingMore: q.isFetchingNextPage,
    loadMoreError: q.isFetchNextPageError ? q.error : undefined,
    onLoadMore: () => void q.fetchNextPage(),
  };
}

// Массовые действия (спека UI-фундамента §4.5, план UF2b.5): серверных массовых ручек нет — исполнение по
// одной строке (runBulk, bulk.ts), ключ идемпотентности — на строку на действие. Исход строки: выполнено
// или отправлено на подтверждение (202 {approval}); отказ — исключение ApiError.
export type BulkOutcome = 'executed' | 'pending_approval';
// Ключ права — строка каталога (me.permissions; отдельного типа ключей у клиента нет), cityId — город строки.
export type BulkPermission = { perm: string; cityId?: string };

export type BulkAction<T> = {
  id: string;
  label: string; // подпись кнопки в панели — текст из локалей экрана
  tone?: 'default' | 'danger';
  // право САМОГО действия над этой строкой и её город: решение — decide(me, perm, cityId) по городу
  // строки (scoped-право модератора на чужой город — deny: строка пропускается и называется второй
  // частью подтверждения)
  permission: (row: T) => BulkPermission;
  // ConfirmDialog: заголовок с числом затрагиваемых строк; word — что ввести (по умолчанию — само число)
  confirm: { title: (n: number) => string; word?: string };
  run: (row: T, idempotencyKey: string) => Promise<BulkOutcome>;
  concurrency?: number; // параллельно в полёте, по умолчанию BULK_CONCURRENCY (3)
};
