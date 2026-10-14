import { type ReactNode, useEffect, useEffectEvent, useId, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Button } from '../Button';
import { CountBadge } from '../CountBadge';
import { cx } from '../cx';
import { useIsMobile } from '../media';
import { EmptyState, ErrorState, LoadingRows } from '../states';
import styles from './DataTable.module.css';
import { type DataTableProps, SEARCH_DEBOUNCE_MS } from './model';

// Таблица списков админки (спека §9.2, макет 07): поиск, чипы-фильтры, раскрывающийся фильтр,
// сортировка, курсорная пагинация «Показать ещё», «Показано N», «Сбросить фильтры»; состояния —
// скелетон, пусто, ошибка с кодом и «Повторить». Таблица управляемая: значения фильтров и
// сортировки держит экран (обычно в search-параметрах маршрута), данные — его запрос;
// infiniteTableProps(query) переводит useInfiniteQuery в пропсы таблицы. На телефоне строки
// становятся карточками с подписями столбцов.

export function DataTable<T>(props: DataTableProps<T>) {
  const t = useTranslations('table');
  const { rows, error, dirty, onReset } = props;
  const reset = dirty && onReset && (
    <Button variant="link" onClick={onReset}>
      {t('reset')}
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
    body = <Rows {...props} rows={rows} />;
  }

  return (
    <div className={styles.root}>
      <Toolbar {...props} />
      {rows !== undefined && (
        <div className={styles.summary}>
          <span className={styles.shown}>{t('shown', { count: rows.length })}</span>
          {reset}
        </div>
      )}
      {body}
    </div>
  );
}

function Toolbar<T>({ search, chips, filters }: DataTableProps<T>) {
  const t = useTranslations('table');
  const [open, setOpen] = useState(false);
  const panelId = useId();
  if (!search && !chips && !filters) return null;
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
  columns,
  rowKey,
  rows,
  sort,
  onSortChange,
  onRowClick,
  activeRowKey,
  hasMore,
  loadingMore,
  loadMoreError,
  onLoadMore,
}: DataTableProps<T> & { rows: T[] }) {
  const t = useTranslations('table');
  const stacked = useIsMobile();
  const toggle = (column: string) =>
    onSortChange?.({
      column,
      direction: sort?.column === column && sort.direction === 'asc' ? 'desc' : 'asc',
    });

  return (
    <>
      <div className={styles.scroll}>
        {/* Роли — явно: на телефоне строки и ячейки становятся блоками (display: block), и часть
            браузеров иначе снимает с них семантику таблицы. */}
        <table
          role="table"
          aria-label={label}
          className={cx(styles.table, stacked && styles.stacked)}
        >
          <thead role="rowgroup">
            <tr role="row">
              {columns.map((c) => {
                const sorted = sort?.column === c.id ? sort.direction : undefined;
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
                      c.width && styles[c.width],
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
                  {columns.map((c) => (
                    <td
                      key={c.id}
                      role="cell"
                      className={cx(styles.td, c.align === 'end' && styles.end)}
                    >
                      {stacked && (
                        // шапка на телефоне скрыта — подпись столбца в ячейке и видна, и слышна
                        <span className={styles.cellLabel}>{c.header}</span>
                      )}
                      {c.cell(row)}
                    </td>
                  ))}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {hasMore && onLoadMore && (
        <div className={styles.footer}>
          {loadMoreError !== undefined && (
            <p role="alert" className={styles.footerError}>
              {t('load_more_failed')}
            </p>
          )}
          <Button variant="secondary" loading={loadingMore} onClick={onLoadMore}>
            {t('load_more')}
          </Button>
        </div>
      )}
    </>
  );
}
