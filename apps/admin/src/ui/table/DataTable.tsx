import { useRouter } from '@tanstack/react-router';
import { tokens } from '@wf/tokens';
import {
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
  type SyntheticEvent,
  useEffect,
  useEffectEvent,
  useId,
  useRef,
  useState,
} from 'react';
import { useTranslations } from 'use-intl';
import { decide } from '../../app/permissions';
import { useSession } from '../../app/session';
import { Button } from '../Button';
import { CountBadge } from '../CountBadge';
import { cx } from '../cx';
import { Tooltip } from '../hint/Tooltip';
import { useIsCoarse, useIsCompact, useIsMobile } from '../media';
import { EmptyState, ErrorState, LoadingRows } from '../states';
import { useVirtualRows } from '../virtual';
import { BulkBar } from './BulkBar';
import styles from './DataTable.module.css';
import { ColumnsMenu } from './ColumnsMenu';
import {
  type Column,
  columnVisibility,
  type DataTableBulk,
  type DataTableProps,
  type DataTableSelect,
  isHideable,
  SEARCH_DEBOUNCE_MS,
  STICKY_HEADER_FROM,
  VIRTUALIZE_FROM,
} from './model';
import { type ColumnPrefs, type Density, readColumns, useDensity, writeColumns } from './prefs';
import { rememberedRow, rememberRow, restoreKeyOf, scrollRestorationId } from './restore';

// промежуток строк таблицы (border-spacing в DataTable.module.css) — виртуализации нужен числом
const ROW_GAP = 6;

// Таблица списков админки (спека §9.2, макет 07): поиск, чипы-фильтры, раскрывающийся фильтр,
// сортировка, курсорная пагинация «Показать ещё», «Показано N», «Сбросить фильтры»; состояния —
// скелетон, пусто, ошибка с кодом и «Повторить». Таблица управляемая: значения фильтров и
// сортировки держит экран (обычно в search-параметрах маршрута), данные — его запрос;
// infiniteTableProps(query) переводит useInfiniteQuery в пропсы таблицы. На телефоне строки
// становятся карточками с подписями столбцов (layout 'table' — нет: своя горизонтальная прокрутка).

export function DataTable<T>(props: DataTableProps<T>) {
  return props.bulk ? <BulkTable {...props} bulk={props.bulk} /> : <TableView {...props} />;
}

function TableView<T>(
  props: DataTableProps<T> & { bulkBar?: ReactNode; selection?: Selection<T> },
) {
  const t = useTranslations('table');
  const ta = useTranslations('actions');
  const { rows, error, dirty, onReset, columns, tableId } = props;
  // ключ позиции «Назад» (restore.ts): по умолчанию — адрес роутера; вне роутера (витрина) — только явный
  const router = useRouter({ warn: false });
  const at = router?.latestLocation;
  const restoreKey = props.restoreKey ?? (at ? restoreKeyOf(at) : undefined);
  const mobile = useIsMobile();
  const compact = useIsCompact();
  const [density, setDensity] = useDensity();
  const coarse = useIsCoarse();
  // Предпочтения столбцов: из storage при монтировании и заново при смене tableId или набора столбцов
  // (вкладки экрана на одном DataTable, столбец пришёл с правом) — иначе выбор одной таблицы ушёл бы в ключ другой.
  const ids = columns.map((c) => c.id);
  const source = `${tableId ?? ''}\n${ids.join('\n')}`;
  const load = (): ColumnPrefs => (tableId ? readColumns(tableId, ids) : { hidden: [], shown: [] });
  const [prefs, setPrefs] = useState<ColumnPrefs>(load);
  const [loaded, setLoaded] = useState(source);
  if (loaded !== source) {
    setLoaded(source);
    setPrefs(load());
  }
  const stacked = (props.layout ?? 'auto') === 'auto' && mobile;
  // 768–1023 по окну (не по контейнеру: открытый сайдбар или панель решение не меняют — спека §3.4, N1)
  const vis = columnVisibility(columns, prefs, compact && !mobile);
  const toggle = (id: string) => {
    const col = columns.find((c) => c.id === id);
    if (!col || !tableId) return;
    const without = (ids: string[]) => ids.filter((x) => x !== id);
    const next: ColumnPrefs = vis.visible.some((c) => c.id === id)
      ? { hidden: [...without(prefs.hidden), id], shown: without(prefs.shown) }
      : {
          hidden: without(prefs.hidden),
          // включённый явно 'low' ширина больше не прячет
          shown: col.priority === 'low' ? [...without(prefs.shown), id] : prefs.shown,
        };
    setPrefs(next);
    writeColumns(tableId, next, ids);
  };
  const menu =
    tableId && !stacked && !props.select && !props.card ? (
      <ColumnsMenu
        items={columns.map((c, i) => {
          const checked = vis.visible.some((v) => v.id === c.id);
          return {
            id: c.id,
            header: c.header,
            checked,
            byWidth: vis.byWidth.has(c.id),
            disabled: !isHideable(c, i) || (checked && vis.visible.length === 1),
          };
        })}
        onToggle={toggle}
        density={density}
        onDensity={setDensity}
        showDensity={!coarse}
      />
    ) : undefined;
  const reset = dirty && onReset && (
    <Button variant="link" onClick={onReset}>
      {ta('resetFilters')}
    </Button>
  );

  let body: ReactNode;
  if (rows === undefined) {
    body = error ? (
      <ErrorState error={error} onRetry={props.onRetry} retrying={props.retrying} />
    ) : (
      <LoadingRows />
    );
  } else if (rows.length === 0) {
    body = dirty ? <EmptyState action={reset} /> : (props.empty ?? <EmptyState />);
  } else if (props.select) {
    body = <Options {...props} rows={rows} select={props.select} />;
  } else if (props.card) {
    body = <Cards {...props} rows={rows} card={props.card} restoreKey={restoreKey} />;
  } else {
    const rowsProps: RowsProps<T> = {
      ...props,
      rows,
      restoreKey,
      stacked,
      density,
      // телефон (карточки): выбор столбцов не действует, 'low' — под «Подробнее»
      shown: stacked ? columns.filter((c) => c.priority !== 'low') : vis.visible,
      details: stacked ? columns.filter((c) => c.priority === 'low') : [],
    };
    body = <Rows {...rowsProps} selection={props.selection} />;
  }

  return (
    <div className={cx(styles.root, props.select && styles.fill)} data-table-root="">
      <Toolbar {...props} menu={menu} />
      {rows !== undefined && (props.summary !== false || reset) && (
        <div className={styles.summary}>
          {props.summary !== false && (
            <span className={styles.shown}>{t('shown', { count: rows.length })}</span>
          )}
          {reset}
        </div>
      )}
      {props.bulkBar}
      {body}
    </div>
  );
}

function Toolbar<T>({ search, chips, filters, menu }: DataTableProps<T> & { menu?: ReactNode }) {
  const t = useTranslations('table');
  const [open, setOpen] = useState(false);
  const panelId = useId();
  if (!search && !chips && !filters && !menu) return null;
  return (
    <>
      <div className={styles.toolbar}>
        {search && <SearchBox {...search} />}
        {chips && (
          <div role="group" aria-label={chips.label} className={styles.chips}>
            {chips.options.map((o) => (
              <button
                key={o.value}
                type="button"
                className={styles.chip}
                aria-pressed={o.value === chips.value}
                onClick={() => chips.onChange(o.value)}
              >
                {o.label}
              </button>
            ))}
          </div>
        )}
        {filters && (
          <button
            type="button"
            className={cx(styles.moreButton, filters.active > 0 && styles.withCount)}
            aria-label={
              filters.active > 0
                ? t('more_filters_active', { count: filters.active })
                : t('more_filters')
            }
            aria-expanded={open}
            aria-controls={panelId}
            onClick={() => setOpen((v) => !v)}
          >
            <svg
              viewBox="0 0 24 24"
              width="15"
              height="15"
              aria-hidden="true"
              className={styles.icon}
            >
              <path d="M3 6 H21 M6.5 12 H17.5 M10 18 H14" />
            </svg>
            <CountBadge count={filters.active} active />
          </button>
        )}
        {menu && <div className={cx(styles.menuSlot, filters && styles.afterFilters)}>{menu}</div>}
      </div>
      {filters && (
        <div id={panelId} hidden={!open} className={styles.panel}>
          {filters.content}
        </div>
      )}
    </>
  );
}

// Поиск: набор держится здесь и уходит экрану после паузы; сброс снаружи (value) — сразу в поле. Экспорт —
// поиск страницы без таблицы строк (матрица флагов, Task 22): то же поле, та же клавиша «/».
export function SearchBox({
  value,
  onChange,
  placeholder,
}: NonNullable<DataTableProps<unknown>['search']>) {
  const t = useTranslations('table');
  const [text, setText] = useState(value);
  const [outer, setOuter] = useState(value);
  // Отправленное экрану, но ещё не вернувшееся в value (экран применяет с задержкой —
  // search-параметр маршрута). Эхо из этого списка не трогает набранное после отправки и снимает
  // себя и более ранние отправки — по последнему вхождению: при схлопнутых навигациях одно значение
  // могло уйти дважды («ab» → «abc» → «ab»), и эхо относится к последней отправке; любое другое value (сброс фильтров, «Назад») — внешнее: оно
  // идёт в поле, а список очищается, чтобы тот же текст, набранный снова, ушёл в запрос.
  const [sent, setSent] = useState<string[]>([]);
  if (outer !== value) {
    setOuter(value);
    const echo = sent.lastIndexOf(value);
    if (echo === -1) setText(value);
    setSent(echo === -1 ? [] : sent.slice(echo + 1));
  }
  const emit = useEffectEvent((v: string) => {
    setSent((list) => [...list, v]);
    onChange(v);
  });
  const last = sent.at(-1);
  useEffect(() => {
    if (text === outer || text === last) return;
    const timer = setTimeout(() => emit(text), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [text, outer, last]);

  return (
    <div className={styles.search}>
      <svg
        viewBox="0 0 24 24"
        width="15"
        height="15"
        aria-hidden="true"
        className={cx(styles.icon, styles.searchIcon)}
      >
        <circle cx="11" cy="11" r="6.6" />
        <path d="M15.8 15.8 L20.4 20.4" />
      </svg>
      <input
        type="search"
        className={styles.searchInput}
        aria-label={t('search')}
        data-page-search="" // клавиша «/» каркаса (model.ts PAGE_SEARCH_ATTR)
        placeholder={placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      {text !== '' && (
        <button
          type="button"
          className={styles.clear}
          aria-label={t('clear_search')}
          onClick={() => {
            setText('');
            setSent((list) => [...list, '']);
            onChange('');
          }}
        >
          <svg
            viewBox="0 0 24 24"
            width="10"
            height="10"
            aria-hidden="true"
            className={styles.icon}
          >
            <path d="M6 6 L18 18 M18 6 L6 18" />
          </svg>
        </button>
      )}
    </div>
  );
}

type RowsProps<T> = DataTableProps<T> & {
  rows: T[];
  restoreKey?: string;
  stacked: boolean;
  density: Density;
  shown: Column<T>[];
  details: Column<T>[];
};

// Выбор строк для массовых действий (BulkTable → Rows): флажок в строке и «Выбрать видимые» в шапке.
type Selection<T> = {
  isSelected: (key: string) => boolean;
  selectable: (row: T) => boolean;
  toggle: (row: T) => void;
  all: 'empty' | 'none' | 'some' | 'all'; // empty — выбирать нечего
  toggleAll: () => void;
  rowLabel: (row: T) => string;
};

// Таблица с массовыми действиями: право — на каждую строку и по её городу (decide по me сессии —
// подписка: права сменились — флажки следуют); строка, где у сотрудника нет права ни на одно действие,
// не выбирается. «Выбрать видимые» — все загруженные доступные строки (и при виртуализации — не только
// те, что в DOM). Выбранные, которых больше нет в данных или права на которые сняли, снимаются с выбора
// (onChange экрану); в BulkBar уходят только присутствующие и доступные. BulkBar стоит на уровне таблицы,
// вне ветки строк: поиск или чип посреди пачки (строк нет, скелетон) её не отменяют (ревью uf2b1w2, m1).
// Версия выбора меняется только от действий пользователя (флажок, «Выбрать видимые», «Снять выбор»): итог
// пачки снимается ими, а не чисткой выбора после перечитывания списка (ревью uf2b1w2, I1).
function BulkTable<T>(props: DataTableProps<T> & { bulk: DataTableBulk<T> }) {
  const { bulk, rows, rowKey } = props;
  const session = useSession();
  const me = session.status === 'active' ? session.me : null;
  const [version, setVersion] = useState(0);
  const selectable = (row: T) =>
    me !== null &&
    bulk.actions.some((a) => {
      try {
        const { perm, cityId } = a.permission(row);
        return decide(me, perm, cityId) !== 'deny';
      } catch {
        return false; // permission(row) бросил — как в BulkBar и runBulk
      }
    });
  const open = (rows ?? []).filter(selectable);
  const openKeys = new Set(open.map(rowKey));
  // строк нет (загрузка с новым фильтром) — выбор не трогаем: чистит только перечитанный список
  const kept =
    rows === undefined ? [...bulk.selected] : [...bulk.selected].filter((k) => openKeys.has(k));
  const stale = kept.length !== bulk.selected.size;
  const keptSig = kept.join('\n');
  const prune = useEffectEvent(() => bulk.onChange(new Set(kept)));
  useEffect(() => {
    if (stale) prune();
  }, [stale, keptSig]);
  const change = (next: ReadonlySet<string>) => {
    setVersion((v) => v + 1);
    bulk.onChange(next);
  };

  const selection: Selection<T> = {
    isSelected: (key) => bulk.selected.has(key) && openKeys.has(key),
    selectable,
    toggle: (row) => {
      if (!selectable(row)) return;
      const key = rowKey(row);
      const next = new Set(kept);
      if (!next.delete(key)) next.add(key);
      change(next);
    },
    all:
      open.length === 0
        ? 'empty'
        : kept.length === 0
          ? 'none'
          : kept.length === open.length
            ? 'all'
            : 'some',
    toggleAll: () => {
      if (open.length === 0) return;
      change(kept.length === open.length ? new Set() : openKeys);
    },
    rowLabel: bulk.rowLabel,
  };
  const bar = (
    <BulkBar
      rows={(rows ?? []).filter((r) => selection.isSelected(rowKey(r)))}
      rowLabel={bulk.rowLabel}
      rowKey={rowKey}
      selectionVersion={version}
      actions={bulk.actions}
      onClear={() => change(new Set())}
      focusAfterClose={(bar) => focusAfterBar(bar.closest<HTMLElement>('[data-table-root]'))}
      onPending={bulk.onPending}
      onSettled={bulk.onSettled}
    />
  );
  return <TableView {...props} bulkBar={bar} selection={selection} />;
}

// Панель снята «Снять выбор» или «Закрыть» — фокус туда, где выбор и продолжается работа (m8): «Выбрать
// видимые» в шапке; шапка скрыта (карточки телефона: display: none — браузер фокус не даёт) — флажок первой
// строки; строк нет (пусто после перечитывания) — сама область таблицы, временно с tabindex="-1" (не цель Tab
// и щелчка: атрибут снимается при уходе фокуса).
function focusAfterBar(root: HTMLElement | null) {
  if (!root) return;
  for (const el of root.querySelectorAll<HTMLElement>('[data-bulk-check]')) {
    el.focus();
    if (document.activeElement === el) return;
  }
  root.tabIndex = -1;
  root.addEventListener('blur', () => root.removeAttribute('tabindex'), { once: true });
  root.focus();
}

// Флажок выбора: недоступный — aria-disabled (не disabled: остаётся в Tab-порядке, причина слышна и видна
// по фокусу и наведению — Tooltip), щелчок и пробел значения не меняют. Щелчок не открывает строку.
// Касание недоступного (iOS не фокусирует флажок по тапу) — фокус на нём сам: подсказка видна и на
// телефоне (ревью uf2b1w2, m3). denied — текст причины или false.
function Check({
  label,
  checked,
  mixed = false,
  denied = false,
  onToggle,
}: {
  label: string;
  checked: boolean;
  mixed?: boolean;
  denied?: string | false;
  onToggle: () => void;
}) {
  const input = (
    <input
      type="checkbox"
      className={styles.check}
      data-bulk-check=""
      aria-label={label}
      checked={checked}
      aria-disabled={denied ? true : undefined}
      ref={(el) => {
        if (el) el.indeterminate = mixed;
      }}
      // щелчок не открывает строку (onRowClick); недоступный — значение управляемое, onToggle не зовём:
      // React возвращает прежнее checked
      onClick={(e) => {
        e.stopPropagation();
        if (denied) e.currentTarget.focus();
      }}
      onChange={() => {
        if (!denied) onToggle();
      }}
    />
  );
  return denied ? <Tooltip content={denied}>{input}</Tooltip> : input;
}

// Строка, с которой ушли (щелчок или Enter), — запомнить под ключом позиции (restore.ts).
function rememberFrom(restoreKey: string | undefined) {
  return (e: SyntheticEvent) => {
    if (!restoreKey) return;
    if ('key' in e && e.key !== 'Enter' && e.key !== ' ') return;
    const el = (e.target as Element).closest?.('[data-row-key]');
    const key = el?.getAttribute('data-row-key');
    if (key != null && e.currentTarget.contains(el ?? null)) rememberRow(restoreKey, key);
  };
}

// флажок выбора (data-bulk-check) — не цель возврата: фокус — на действии строки (ревью uf2b1w2, m6)
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([data-bulk-check]), [tabindex="0"]';

// Возврат на позицию: строка, с которой ушли, получает фокус, если таблица смонтирована с тем же ключом,
// а фокус потерян (на body — уход «Назад» снял прежний экран); фокус где-то есть (перешли по меню) — не
// перехватываем. Строка вне окна виртуализации — сначала прокрутка к ней (после эффекта: прокрутка
// перерисовывает окно синхронно). Строки больше нет — забываем.
function useRestoreFocus<T>(
  restoreKey: string | undefined,
  rows: T[],
  rowKey: (row: T) => string,
  container: RefObject<HTMLElement | null>,
  scrollToIndex?: (i: number) => void,
) {
  const [initial] = useState(() => (restoreKey ? rememberedRow(restoreKey) : undefined));
  const pending = useRef(initial);
  const scrolled = useRef(false);
  useEffect(() => {
    const key = pending.current;
    if (key === undefined) return;
    const active = document.activeElement;
    if (active && active !== document.body) {
      pending.current = undefined;
      return;
    }
    const el = [...(container.current?.querySelectorAll<HTMLElement>('[data-row-key]') ?? [])].find(
      (x) => x.dataset.rowKey === key,
    );
    if (el) {
      pending.current = undefined;
      const target = el.matches('[tabindex="0"]') ? el : el.querySelector<HTMLElement>(FOCUSABLE);
      target?.focus();
      return;
    }
    const index = rows.findIndex((r) => rowKey(r) === key);
    if (index === -1 || !scrollToIndex) {
      pending.current = undefined;
      return;
    }
    if (scrolled.current) return;
    scrolled.current = true;
    queueMicrotask(() => scrollToIndex(index));
  });
}

function Rows<T>({
  label,
  rowKey,
  rows,
  sort,
  onSortChange,
  onRowClick,
  activeRowKey,
  layout = 'auto',
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
  stickyHeader,
  stickyFirstColumn,
  virtualize,
  restoreKey,
  stacked,
  density,
  shown,
  details,
  selection,
}: RowsProps<T> & { selection?: Selection<T> }) {
  const t = useTranslations('table');
  const base = useId();
  const scrollRef = useRef<HTMLDivElement>(null);
  // карточки телефона, раскрытые «Подробнее»
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const sticky = stickyHeader ?? rows.length > STICKY_HEADER_FROM;
  // в карточках телефона закреплять нечего: строка — блок
  const pin = !!stickyFirstColumn && !stacked;
  const pinWidth = shown[0]?.width ?? 'lg';
  // Виртуализация — у строк таблицы; карточки телефона (строка — блок) — content-visibility в CSS.
  const virtual =
    !stacked && (virtualize === undefined ? rows.length > VIRTUALIZE_FROM : virtualize !== false);
  const estimate =
    (typeof virtualize === 'object' ? virtualize.estimateRow : undefined) ??
    tokens.density[density].row;
  const { items, scrollToIndex, measure, placeGap } = useVirtualRows({
    count: virtual ? rows.length : 0,
    estimate,
    scrollRef,
    gap: ROW_GAP,
    getItemKey: (i) => {
      const row = rows[i];
      return row === undefined ? i : rowKey(row);
    },
  });
  useRestoreFocus(restoreKey, rows, rowKey, scrollRef, virtual ? scrollToIndex : undefined);
  const visible = virtual
    ? items.flatMap((it) => {
        const row = rows[it.index];
        return row === undefined ? [] : [{ row, index: it.index }];
      })
    : rows.map((row, index) => ({ row, index }));
  const span = shown.length + (selection ? 1 : 0);
  const toggle = (column: string) =>
    onSortChange?.({
      column,
      direction: sort?.column === column && sort.direction === 'asc' ? 'desc' : 'asc',
    });
  const toggleDetails = (key: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      return next;
    });
  const remember = rememberFrom(restoreKey);

  return (
    <>
      <div
        ref={scrollRef}
        className={cx(
          styles.scroll,
          stacked && styles.flow,
          virtual && styles.virtual,
          pin && styles[`pin-${pinWidth}`],
        )}
        data-table-scroll=""
        data-scroll-restoration-id={restoreKey ? scrollRestorationId(restoreKey) : undefined}
        onClickCapture={remember}
        onKeyDownCapture={remember}
      >
        {/* Роли — явно: на телефоне строки и ячейки становятся блоками (display: block), и часть
            браузеров иначе снимает с них семантику таблицы. Виртуально — aria-rowcount (шапка — строка 1)
            и aria-rowindex у строк: читалка знает размер и позицию. */}
        <table
          role="table"
          aria-label={label}
          aria-rowcount={virtual ? rows.length + 1 : undefined}
          data-density={density}
          className={cx(
            styles.table,
            stacked && styles.stacked,
            layout === 'table' && styles.scrolls,
            sticky && styles.stickyHeader,
            density === 'compact' && styles.compact,
          )}
        >
          <thead role="rowgroup">
            <tr role="row" aria-rowindex={virtual ? 1 : undefined}>
              {selection && (
                <th role="columnheader" scope="col" className={cx(styles.th, styles.checkCell)}>
                  <Check
                    label={t('bulk.select_all')}
                    checked={selection.all === 'all'}
                    mixed={selection.all === 'some'}
                    denied={selection.all === 'empty' && t('bulk.select_none')}
                    onToggle={selection.toggleAll}
                  />
                </th>
              )}
              {shown.map((c, i) => {
                const sorted = sort?.column === c.id ? sort.direction : undefined;
                const width = c.width ?? (pin && i === 0 ? pinWidth : undefined);
                return (
                  <th
                    role="columnheader"
                    key={c.id}
                    scope="col"
                    aria-sort={
                      sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : undefined
                    }
                    className={cx(
                      styles.th,
                      c.align === 'end' && styles.end,
                      width && styles[width],
                      pin && i === 0 && styles.pinned,
                    )}
                  >
                    {c.sortable && onSortChange ? (
                      <button
                        type="button"
                        className={cx(
                          styles.sortButton,
                          sorted && styles.sorted,
                          sorted === 'desc' && styles.desc,
                        )}
                        onClick={() => toggle(c.id)}
                      >
                        {c.header}
                        <svg
                          viewBox="0 0 24 24"
                          width="9"
                          height="9"
                          aria-hidden="true"
                          className={cx(styles.icon, styles.arrow)}
                        >
                          <path d="M6 10 L12 16 L18 10" />
                        </svg>
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody role="rowgroup">
            {virtual && (
              // распорка до окна: строки таблицы не позиционируются абсолютно
              <tr aria-hidden="true" className={styles.gapRow}>
                <td ref={(el) => placeGap(el, 'before')} colSpan={span} className={styles.gap} />
              </tr>
            )}
            {visible.map(({ row, index }) => {
              const key = rowKey(row);
              const expanded = open.has(key);
              const detailId = (c: Column<T>) => `${base}-${key}-${c.id}`;
              const selected = selection?.isSelected(key) ?? false;
              return (
                <tr
                  key={key}
                  role="row"
                  data-row-key={key}
                  data-index={virtual ? index : undefined}
                  ref={virtual ? measure : undefined}
                  aria-rowindex={virtual ? index + 2 : undefined}
                  className={cx(
                    styles.row,
                    onRowClick && styles.clickable,
                    key === activeRowKey && styles.active,
                    selected && styles.selected,
                  )}
                  // строка открывает карточку: мышью и с клавиатуры (Enter, пробел)
                  tabIndex={onRowClick ? 0 : undefined}
                  aria-current={key === activeRowKey ? 'true' : undefined}
                  onClick={onRowClick && (() => onRowClick(row))}
                  onKeyDown={
                    onRowClick &&
                    ((e) => {
                      if (e.target !== e.currentTarget) return;
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onRowClick(row);
                      }
                    })
                  }
                >
                  {selection && (
                    <td role="cell" className={cx(styles.td, styles.checkCell)}>
                      <Check
                        label={t('bulk.select_row', { name: selection.rowLabel(row) })}
                        checked={selected}
                        denied={!selection.selectable(row) && t('bulk.row_denied')}
                        onToggle={() => selection.toggle(row)}
                      />
                    </td>
                  )}
                  {shown.map((c, i) => (
                    <td
                      key={c.id}
                      role="cell"
                      className={cx(
                        styles.td,
                        c.align === 'end' && styles.end,
                        // закреплённый — ширина и у ячеек: переносом длинного она не растёт сверх .pin-*
                        pin && i === 0 && styles.pinned,
                        pin && i === 0 && styles[pinWidth],
                      )}
                    >
                      {stacked && (
                        // шапка на телефоне скрыта — подпись столбца в ячейке и видна, и слышна
                        <span className={styles.cellLabel}>{c.header}</span>
                      )}
                      {c.cell(row)}
                    </td>
                  ))}
                  {details.map((c) => (
                    <td
                      key={c.id}
                      id={detailId(c)}
                      role="cell"
                      hidden={!expanded}
                      className={styles.td}
                    >
                      <span className={styles.cellLabel}>{c.header}</span>
                      {c.cell(row)}
                    </td>
                  ))}
                  {details.length > 0 && (
                    <td role="cell" className={cx(styles.td, styles.detailsCell)}>
                      <Button
                        variant="link"
                        aria-expanded={expanded}
                        aria-controls={details.map(detailId).join(' ')}
                        onClick={(e) => {
                          // строка с onRowClick не открывается от «Подробнее»
                          e.stopPropagation();
                          toggleDetails(key);
                        }}
                      >
                        {t('columns.details')}
                      </Button>
                    </td>
                  )}
                </tr>
              );
            })}
            {virtual && (
              <tr aria-hidden="true" className={styles.gapRow}>
                <td ref={(el) => placeGap(el, 'after')} colSpan={span} className={styles.gap} />
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <LoadMore {...{ hasMore, loadingMore, loadMoreError, onLoadMore }} />
    </>
  );
}

// Карточки вместо строк: список с именем таблицы, карточку рисует экран. Длинный список не
// виртуализируется — content-visibility у карточки (CSS); возврат «Назад» — фокус на карточке, с которой
// ушли (её первое фокусируемое — «Открыть»).
function Cards<T>({
  label,
  rowKey,
  rows,
  card,
  restoreKey,
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: DataTableProps<T> & { rows: T[]; card: (row: T) => ReactNode; restoreKey?: string }) {
  const listRef = useRef<HTMLUListElement>(null);
  useRestoreFocus(restoreKey, rows, rowKey, listRef);
  const remember = rememberFrom(restoreKey);
  return (
    <>
      <ul
        ref={listRef}
        aria-label={label}
        className={styles.cards}
        onClickCapture={remember}
        onKeyDownCapture={remember}
      >
        {rows.map((row) => (
          <li key={rowKey(row)} data-row-key={rowKey(row)} className={styles.cardItem}>
            {card(row)}
          </li>
        ))}
      </ul>
      <LoadMore {...{ hasMore, loadingMore, loadMoreError, onLoadMore }} />
    </>
  );
}

// Список выбора (select): фокус — на самом списке (одна остановка Tab), выбранная строка —
// aria-activedescendant; стрелки, Home/End выбирают (выбор следует за фокусом), Enter — onActivate.
// Выбранная строка прокручивается в видимую часть списка. Виртуально (> VIRTUALIZE_FROM): перед выбором
// стрелкой — scrollToIndex, чтобы строка была в DOM к смене aria-activedescendant; ссылка на строку вне
// DOM не ставится никогда.
function Options<T>({
  label,
  rowKey,
  rows,
  select,
  virtualize,
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: DataTableProps<T> & { rows: T[]; select: DataTableSelect<T> }) {
  const base = useId();
  const listRef = useRef<HTMLDivElement>(null);
  const optionId = (i: number) => `${base}-option-${i}`;
  const index = rows.findIndex((r) => rowKey(r) === select.value);
  const virtual = virtualize === undefined ? rows.length > VIRTUALIZE_FROM : virtualize !== false;
  const { items, scrollToIndex, measure, place, placeTotal } = useVirtualRows({
    count: virtual ? rows.length : 0,
    estimate:
      (typeof virtualize === 'object' ? virtualize.estimateRow : undefined) ?? tokens.size.control,
    scrollRef: listRef,
    getItemKey: (i) => {
      const row = rows[i];
      return row === undefined ? i : rowKey(row);
    },
  });
  const rendered = !virtual || items.some((it) => it.index === index);
  const activeId = index === -1 || !rendered ? undefined : optionId(index);
  useEffect(() => {
    // jsdom не умеет scrollIntoView — в тестах вызова нет
    if (activeId && !virtual)
      document.getElementById(activeId)?.scrollIntoView?.({ block: 'nearest' });
  }, [activeId, virtual]);
  // выбранная при открытии строка вне окна — прокрутить к ней (после эффекта: не во время отрисовки)
  const revealInitial = useEffectEvent(() => {
    if (virtual && index > 0) queueMicrotask(() => scrollToIndex(index));
  });
  useEffect(() => revealInitial(), []);

  const onKeyDown = (e: KeyboardEvent) => {
    const last = rows.length - 1;
    const current = rows[index];
    if (e.key === 'Enter') {
      if (current && select.onActivate) {
        e.preventDefault();
        select.onActivate(current);
      }
      return;
    }
    const next =
      e.key === 'ArrowDown'
        ? Math.min(index + 1, last)
        : e.key === 'ArrowUp'
          ? Math.max(index - 1, 0)
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? last
              : -1;
    const row = rows[next];
    if (!row) return;
    e.preventDefault();
    if (virtual) scrollToIndex(next);
    select.onChange(row);
  };

  const option = (row: T, i: number, virtualItem?: { start: number; size: number }) => (
    <div
      key={rowKey(row)}
      id={optionId(i)}
      role="option"
      aria-selected={i === index}
      aria-setsize={virtual ? rows.length : undefined}
      aria-posinset={virtual ? i + 1 : undefined}
      data-index={virtual ? i : undefined}
      ref={
        virtualItem
          ? (el) => {
              place(el, { index: i, ...virtualItem });
              measure(el);
            }
          : undefined
      }
      className={cx(styles.option, virtual && styles.virtualOption)}
      onClick={() => select.onChange(row)}
      onDoubleClick={select.onActivate && (() => select.onActivate?.(row))}
    >
      {select.option(row)}
    </div>
  );

  return (
    <>
      <div
        ref={listRef}
        role="listbox"
        aria-label={label}
        aria-activedescendant={activeId}
        tabIndex={0}
        className={styles.options}
        onKeyDown={onKeyDown}
      >
        {virtual ? (
          <div ref={placeTotal} role="presentation" className={styles.optionsTrack}>
            {items.map((it) => {
              const row = rows[it.index];
              return row === undefined ? null : option(row, it.index, it);
            })}
          </div>
        ) : (
          rows.map((row, i) => option(row, i))
        )}
      </div>
      <LoadMore {...{ hasMore, loadingMore, loadMoreError, onLoadMore }} />
    </>
  );
}

// «Показать ещё» под данными; не удалось — текст ошибки, строки остаются.
function LoadMore({
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: Pick<DataTableProps<unknown>, 'hasMore' | 'loadingMore' | 'loadMoreError' | 'onLoadMore'>) {
  const t = useTranslations('table');
  const ta = useTranslations('actions');
  if (!hasMore || !onLoadMore) return null;
  return (
    <div className={styles.footer}>
      {loadMoreError !== undefined && (
        <p role="alert" className={styles.footerError}>
          {t('load_more_failed')}
        </p>
      )}
      <Button variant="secondary" loading={loadingMore} onClick={onLoadMore}>
        {ta('showMore')}
      </Button>
    </div>
  );
}
