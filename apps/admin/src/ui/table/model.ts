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
  onRowClick?: (row: T) => void;
  activeRowKey?: string; // строка, открытая в панели справа
};

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
