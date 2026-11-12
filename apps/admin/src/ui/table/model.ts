import type { InfiniteData, UseInfiniteQueryResult } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import type { BulkReport } from './bulk';

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

// Режимы строк взаимоисключающие (план UF2b.4): список выбора (select), карточки (card) или строки с
// массовыми действиями (bulk) — тип не даёт задать два сразу. Карточки вместо строк (card — сетка, макет
// «Города и запуск»): та же панель, сводка, «Показать ещё» и состояния; данные — список с именем label,
// columns не используются.
export type DataTableProps<T> = DataTableBase<T> &
  (
    | { select: DataTableSelect<T>; card?: never; bulk?: never }
    | { card: (row: T) => ReactNode; select?: never; bulk?: never }
    | { bulk?: DataTableBulk<T>; select?: never; card?: never }
  );

// Список выбора вместо строк (шаг «найти место» в панели): role="listbox", строка — option с
// выделением, клик и стрелки выбирают, Enter и двойной клик — главное действие (onActivate).
// Прокручивается только сам список (родитель задаёт высоту), действие — в подвале панели у экрана.
export type DataTableSelect<T> = {
  value: string | null; // rowKey выбранной строки
  onChange: (row: T) => void;
  onActivate?: (row: T) => void;
  option: (row: T) => ReactNode;
};

// Массовые действия (спека UI-фундамента §4.5): выбор держит экран (сбрасывается с ним — каркас
// key={me.id}); флажок в строке и «Выбрать видимые» в шапке (загруженные строки, кроме тех, где у
// сотрудника нет права ни на одно действие — по городу строки); панель BulkBar — при выборе ≥ 1.
// Выбранные строки, которых нет в данных после перечитывания (или права на которые сняли), таблица
// снимает с выбора сама (onChange).
export type DataTableBulk<T> = {
  selected: ReadonlySet<string>; // rowKey выбранных
  onChange: (next: ReadonlySet<string>) => void;
  actions: BulkAction<T>[];
  rowLabel: (row: T) => string; // имя строки: флажок «Выбрать: …» и список ошибок итога
  onPending: () => unknown; // после новых 202 — экран передаёт useInvalidateApprovals()
  onSettled?: (report: BulkReport<T>) => unknown; // после пачки — перечитать список один раз
};

type DataTableBase<T> = {
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
  onRowClick?: (row: T) => void;
  activeRowKey?: string; // строка, открытая в панели справа
  // Закрепление (спека UI-фундамента §4.5): шапка липнет внутри своей области прокрутки (по умолчанию — при
  // строках > STICKY_HEADER_FROM), первый столбец — у широких таблиц (матрица прав, и при layout 'table' на
  // телефоне). Ширина закреплённого столбца — его width (по умолчанию 'lg').
  stickyHeader?: boolean;
  stickyFirstColumn?: boolean;
  // Меню «Столбцы» и плотность: без tableId кнопки нет; выбор — localStorage wf.admin.table.<tableId>.
  tableId?: string;
  // Виртуализация (§4.5): DOM держит видимые строки ± запас, у таблицы aria-rowcount, у строк
  // aria-rowindex; строки разной высоты меряются по факту. По умолчанию — при строках > VIRTUALIZE_FROM;
  // estimateRow — ожидаемая высота строки (по умолчанию — высота строки плотности). Строки и список
  // выбора; карточки (card, строки-карточки телефона) — content-visibility в CSS, без JS.
  virtualize?: boolean | { estimateRow?: number };
  // Ключ позиции для возврата «Назад» (restore.ts): по умолчанию путь + search без ключей панели.
  restoreKey?: string;
};

// Шапка закрепляется по умолчанию, когда строк больше этого.
export const STICKY_HEADER_FROM = 20;
// Строки виртуализируются по умолчанию, когда их больше этого.
export const VIRTUALIZE_FROM = 200;

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
  DataTableBase<T>,
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
