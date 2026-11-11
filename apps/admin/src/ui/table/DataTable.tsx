import {
  type KeyboardEvent,
  type ReactNode,
  useEffect,
  useEffectEvent,
  useId,
  useState,
} from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { CountBadge } from '../CountBadge';
import { cx } from '../cx';
import { useIsCoarse, useIsCompact, useIsMobile } from '../media';
import { EmptyState, ErrorState, LoadingRows } from '../states';
import styles from './DataTable.module.css';
import { ColumnsMenu } from './ColumnsMenu';
import {
  type Column,
  columnVisibility,
  type DataTableProps,
  isHideable,
  SEARCH_DEBOUNCE_MS,
  STICKY_HEADER_FROM,
} from './model';
import { type ColumnPrefs, type Density, readColumns, useDensity, writeColumns } from './prefs';

// Таблица списков админки (спека §9.2, макет 07): поиск, чипы-фильтры, раскрывающийся фильтр,
// сортировка, курсорная пагинация «Показать ещё», «Показано N», «Сбросить фильтры»; состояния —
// скелетон, пусто, ошибка с кодом и «Повторить». Таблица управляемая: значения фильтров и
// сортировки держит экран (обычно в search-параметрах маршрута), данные — его запрос;
// infiniteTableProps(query) переводит useInfiniteQuery в пропсы таблицы. На телефоне строки
// становятся карточками с подписями столбцов (layout 'table' — нет: своя горизонтальная прокрутка).

export function DataTable<T>(props: DataTableProps<T>) {
  const t = useTranslations('table');
  const ta = useTranslations('actions');
  const { rows, error, dirty, onReset, columns, tableId } = props;
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
  } else {
    body = props.select ? (
      <Options {...props} rows={rows} select={props.select} />
    ) : props.card ? (
      <Cards {...props} rows={rows} card={props.card} />
    ) : (
      <Rows
        {...props}
        rows={rows}
        stacked={stacked}
        density={density}
        // телефон (карточки): выбор столбцов не действует, 'low' — под «Подробнее»
        shown={stacked ? columns.filter((c) => c.priority !== 'low') : vis.visible}
        details={stacked ? columns.filter((c) => c.priority === 'low') : []}
      />
    );
  }

  return (
    <div className={cx(styles.root, props.select && styles.fill)}>
      <Toolbar {...props} menu={menu} />
      {rows !== undefined && (props.summary !== false || reset) && (
        <div className={styles.summary}>
          {props.summary !== false && (
            <span className={styles.shown}>{t('shown', { count: rows.length })}</span>
          )}
          {reset}
        </div>
      )}
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

// Поиск: набор держится здесь и уходит экрану после паузы; сброс снаружи (value) — сразу в поле.
function SearchBox({
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
  stacked,
  density,
  shown,
  details,
}: DataTableProps<T> & {
  rows: T[];
  stacked: boolean;
  density: Density;
  shown: Column<T>[];
  details: Column<T>[];
}) {
  const t = useTranslations('table');
  const base = useId();
  // карточки телефона, раскрытые «Подробнее»
  const [open, setOpen] = useState<ReadonlySet<string>>(() => new Set());
  const sticky = stickyHeader ?? rows.length > STICKY_HEADER_FROM;
  // в карточках телефона закреплять нечего: строка — блок
  const pin = !!stickyFirstColumn && !stacked;
  const pinWidth = shown[0]?.width ?? 'lg';
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

  return (
    <>
      <div
        className={cx(styles.scroll, stacked && styles.flow, pin && styles[`pin-${pinWidth}`])}
        data-table-scroll=""
      >
        {/* Роли — явно: на телефоне строки и ячейки становятся блоками (display: block), и часть
            браузеров иначе снимает с них семантику таблицы. */}
        <table
          role="table"
          aria-label={label}
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
            <tr role="row">
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
            {rows.map((row) => {
              const key = rowKey(row);
              const expanded = open.has(key);
              const detailId = (c: Column<T>) => `${base}-${key}-${c.id}`;
              return (
                <tr
                  key={key}
                  role="row"
                  className={cx(
                    styles.row,
                    onRowClick && styles.clickable,
                    key === activeRowKey && styles.active,
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
          </tbody>
        </table>
      </div>
      <LoadMore {...{ hasMore, loadingMore, loadMoreError, onLoadMore }} />
    </>
  );
}

// Карточки вместо строк: список с именем таблицы, карточку рисует экран.
function Cards<T>({
  label,
  rowKey,
  rows,
  card,
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: DataTableProps<T> & { rows: T[]; card: (row: T) => ReactNode }) {
  return (
    <>
      <ul aria-label={label} className={styles.cards}>
        {rows.map((row) => (
          <li key={rowKey(row)} className={styles.cardItem}>
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
// Выбранная строка прокручивается в видимую часть списка.
function Options<T>({
  label,
  rowKey,
  rows,
  select,
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: DataTableProps<T> & { rows: T[]; select: NonNullable<DataTableProps<T>['select']> }) {
  const base = useId();
  const optionId = (i: number) => `${base}-option-${i}`;
  const index = rows.findIndex((r) => rowKey(r) === select.value);
  const activeId = index === -1 ? undefined : optionId(index);
  useEffect(() => {
    // jsdom не умеет scrollIntoView — в тестах вызова нет
    if (activeId) document.getElementById(activeId)?.scrollIntoView?.({ block: 'nearest' });
  }, [activeId]);

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
    select.onChange(row);
  };

  return (
    <>
      <div
        role="listbox"
        aria-label={label}
        aria-activedescendant={activeId}
        tabIndex={0}
        className={styles.options}
        onKeyDown={onKeyDown}
      >
        {rows.map((row, i) => {
          const selected = i === index;
          return (
            <div
              key={rowKey(row)}
              id={optionId(i)}
              role="option"
              aria-selected={selected}
              className={styles.option}
              onClick={() => select.onChange(row)}
              onDoubleClick={select.onActivate && (() => select.onActivate?.(row))}
            >
              {select.option(row)}
            </div>
          );
        })}
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
