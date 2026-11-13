import { type FocusEvent, type ReactNode, useCallback, useRef, useState } from 'react';
import { setCssPx } from '../cssVar';
import { cx } from '../cx';
import { MATRIX_COL_PX, MATRIX_HEAD_PX, MATRIX_VIRTUALIZE_FROM } from './matrix';
import styles from './Matrix.module.css';
import { useVirtualRows, type VirtualItem } from './useVirtualRows';

// Неподвижный столбец-значение после закреплённого первого («Везде» у флагов).
export type MatrixFixedColumn<R> = { id: string; header: ReactNode; cell: (row: R) => ReactNode };

type Segment<C> =
  { kind: 'gap'; key: string; size: number } | { kind: 'col'; item: VirtualItem; column: C };

// Матрица «строка × столбец» с горизонтальной виртуализацией (спека UI-фундамента §4.5; флаги функций — до 2000
// городов столбцами, Task 22). Первый столбец закреплён (sticky), за ним — неподвижные (fixed), дальше — столбцы-
// значения: при числе > MATRIX_VIRTUALIZE_FROM в DOM только окно ± запас, пропущенное — распорки --wf-virtual-gap.
// Таблица остаётся таблицей: table-layout fixed, ширины — из строки шапки (--wf-matrix-*, --wf-virtual-size через
// setCssPx; CSP — admin.md). Читалка знает размер: aria-colcount у таблицы, aria-colindex у каждой ячейки.
// Клавиатура: столбец с фокусом всегда в DOM (keep — useVirtualRows), focusin прокручивает к нему (Tab по клеткам
// строки уводит окно вслед за фокусом, а не на следующую строку).
export function VirtualMatrix<R, C>({
  label,
  rows,
  rowKey,
  corner,
  rowHeader,
  fixed = [],
  columns,
  columnKey,
  columnHeader,
  cell,
}: {
  label: string;
  rows: readonly R[];
  rowKey: (row: R) => string;
  corner: ReactNode; // шапка первого столбца
  rowHeader: (row: R) => ReactNode; // первая ячейка строки (th scope=row)
  fixed?: MatrixFixedColumn<R>[];
  columns: readonly C[];
  columnKey: (column: C) => string;
  columnHeader: (column: C) => ReactNode;
  cell: (row: R, column: C) => ReactNode;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [keep, setKeep] = useState<number | null>(null);
  const virtual = columns.length > MATRIX_VIRTUALIZE_FROM;
  const lead = MATRIX_HEAD_PX + fixed.length * MATRIX_COL_PX;
  const {
    items,
    totalSize: virtualTotal,
    scrollToIndex,
    place,
  } = useVirtualRows({
    count: virtual ? columns.length : 0,
    estimate: MATRIX_COL_PX,
    scrollRef,
    horizontal: true,
    paddingStart: lead,
    scrollPaddingStart: MATRIX_HEAD_PX,
    keep,
    getItemKey: (i) => {
      const c = columns[i];
      return c === undefined ? i : columnKey(c);
    },
  });
  // меньше порога — все столбцы по порядку, те же числа положения (без окна и распорок)
  const shown: VirtualItem[] = virtual
    ? items
    : columns.map((_, index) => ({
        index,
        start: lead + index * MATRIX_COL_PX,
        size: MATRIX_COL_PX,
      }));
  const totalSize = virtual ? virtualTotal : lead + columns.length * MATRIX_COL_PX;

  // окно с дырами (столбец с фокусом далеко от окна) — распорка на каждую дыру
  const segments: Segment<C>[] = [];
  let edge = lead;
  for (const item of shown) {
    const column = columns[item.index];
    if (column === undefined) continue;
    if (item.start > edge)
      segments.push({ kind: 'gap', key: `gap-${item.index}`, size: item.start - edge });
    segments.push({ kind: 'col', item, column });
    edge = item.start + item.size;
  }
  if (totalSize > edge) segments.push({ kind: 'gap', key: 'gap-end', size: totalSize - edge });

  const sizeTable = useCallback(
    (el: HTMLElement | null) => {
      if (!el) return;
      setCssPx(el, '--wf-virtual-total', totalSize);
      setCssPx(el, '--wf-matrix-head', MATRIX_HEAD_PX);
      setCssPx(el, '--wf-matrix-col', MATRIX_COL_PX);
    },
    [totalSize],
  );
  const colOf = (target: EventTarget | null) => {
    const el = target instanceof Element ? target.closest<HTMLElement>('[data-matrix-col]') : null;
    return el ? Number(el.dataset.matrixCol) : null;
  };
  const onFocus = (e: FocusEvent<HTMLDivElement>) => {
    if (!virtual) return;
    const i = colOf(e.target);
    if (i === null) return;
    setKeep(i);
    scrollToIndex(i, 'auto');
  };
  const onBlur = (e: FocusEvent<HTMLDivElement>) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setKeep(null);
  };
  const colIndex = (i: number) => 2 + fixed.length + i;

  return (
    <div
      ref={scrollRef}
      className={styles.viewport}
      data-matrix-scroll=""
      onFocus={onFocus}
      onBlur={onBlur}
    >
      <table
        ref={sizeTable}
        role="table"
        aria-label={label}
        aria-colcount={1 + fixed.length + columns.length}
        className={styles.table}
      >
        <thead role="rowgroup">
          <tr role="row">
            <th
              role="columnheader"
              scope="col"
              aria-colindex={1}
              className={cx(styles.th, styles.pin)}
            >
              {corner}
            </th>
            {fixed.map((f, i) => (
              <th
                role="columnheader"
                scope="col"
                key={f.id}
                aria-colindex={2 + i}
                className={cx(styles.th, styles.col)}
              >
                {f.header}
              </th>
            ))}
            {segments.map((s) =>
              s.kind === 'gap' ? (
                <td
                  key={s.key}
                  aria-hidden="true"
                  className={styles.gap}
                  ref={(el) => {
                    if (el) setCssPx(el, '--wf-virtual-gap', s.size);
                  }}
                />
              ) : (
                <th
                  role="columnheader"
                  scope="col"
                  key={columnKey(s.column)}
                  data-matrix-col={s.item.index}
                  aria-colindex={colIndex(s.item.index)}
                  className={cx(styles.th, styles.col, styles.placed)}
                  ref={(el) => place(el, s.item)}
                >
                  {columnHeader(s.column)}
                </th>
              ),
            )}
          </tr>
        </thead>
        <tbody role="rowgroup">
          {rows.map((r) => (
            <tr role="row" key={rowKey(r)}>
              <th
                role="rowheader"
                scope="row"
                aria-colindex={1}
                className={cx(styles.td, styles.pin)}
              >
                {rowHeader(r)}
              </th>
              {fixed.map((f, i) => (
                <td
                  role="cell"
                  key={f.id}
                  aria-colindex={2 + i}
                  className={cx(styles.td, styles.col)}
                >
                  {f.cell(r)}
                </td>
              ))}
              {segments.map((s) =>
                s.kind === 'gap' ? (
                  <td key={s.key} aria-hidden="true" className={styles.gap} />
                ) : (
                  <td
                    role="cell"
                    key={columnKey(s.column)}
                    data-matrix-col={s.item.index}
                    aria-colindex={colIndex(s.item.index)}
                    className={cx(styles.td, styles.col)}
                  >
                    {cell(r, s.column)}
                  </td>
                ),
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
