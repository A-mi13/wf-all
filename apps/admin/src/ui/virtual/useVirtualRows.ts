import { defaultRangeExtractor, type Range, useVirtualizer } from '@tanstack/react-virtual';
import { type RefObject, useCallback } from 'react';
import { setCssPx } from '../cssVar';

// Виртуализация строк и списков (спека UI-фундамента §4.5, стр. 573–588): обёртка useVirtualizer —
// DOM держит видимые элементы ± запас. Положение элемента и полная длина — только числами
// --wf-virtual-start/-size/-total через setCssPx (CSP: style-src 'self', страж csp.test.ts; ui/virtual
// — второй и последний разрешённый вызыватель); оформление — Virtual.module.css. horizontal — матрица
// флагов (города столбцами). Элемент, который измеряется по факту (measure), несёт data-index.
// Результат — деструктурировать (const { items, place } = …): правило react-hooks/refs, увидев v.place в
// ref={…}, считает весь объект ref и запрещает читать v.items в рендере.

export type VirtualItem = { index: number; start: number; size: number };

export type VirtualRows = {
  items: VirtualItem[];
  totalSize: number;
  // align 'auto' — прокрутить, только если элемент виден не целиком (фокус клавиатуры)
  scrollToIndex(i: number, align?: 'start' | 'auto'): void;
  // ref-колбэк элемента с data-index: меряет фактический размер (строки разной высоты)
  measure(el: HTMLElement | null): void;
  // ставит элементу его положение и размер (переменные --wf-virtual-start/-size)
  place(el: HTMLElement | null, item: VirtualItem): void;
  // ставит контейнеру полную длину (--wf-virtual-total)
  placeTotal(el: HTMLElement | null): void;
  // ставит строке-распорке таблицы (строки таблицы не позиционируются абсолютно) высоту пропущенного
  // до окна ('before') или после него ('after') — --wf-virtual-gap
  placeGap(el: HTMLElement | null, edge: 'before' | 'after'): void;
};

// Запас по умолчанию: строки за краем окна, чтобы быстрая прокрутка не показывала пустоту.
export const VIRTUAL_OVERSCAN = 5;

export function useVirtualRows({
  count,
  estimate,
  scrollRef,
  overscan = VIRTUAL_OVERSCAN,
  horizontal = false,
  gap = 0,
  getItemKey,
  paddingStart = 0,
  scrollPaddingStart = 0,
  keep = null,
}: {
  count: number;
  estimate: number;
  scrollRef: RefObject<HTMLElement | null>;
  overscan?: number;
  horizontal?: boolean;
  gap?: number; // промежуток между элементами (border-spacing строк таблицы)
  // ключ элемента (rowKey строки): замеры следуют за строкой, а не за индексом — перечитывание с новой
  // строкой сверху не путает высоты
  getItemKey?: (index: number) => string | number;
  // до первого элемента — неподвижное (закреплённые столбцы матрицы): элементы начинаются с этого смещения
  paddingStart?: number;
  // под закреплённым краем окна ничего не видно: scrollToIndex ставит элемент правее него
  scrollPaddingStart?: number;
  // элемент, который всегда в DOM (столбец с фокусом клавиатуры): прокрутка мимо не снимает фокус на body
  keep?: number | null;
}): VirtualRows {
  const rangeExtractor = useCallback(
    (range: Range) => {
      const indexes = defaultRangeExtractor(range);
      if (keep === null || keep < 0 || keep >= range.count || indexes.includes(keep))
        return indexes;
      return [...indexes, keep].sort((a, b) => a - b);
    },
    [keep],
  );
  const v = useVirtualizer({
    count,
    estimateSize: () => estimate,
    getScrollElement: () => scrollRef.current,
    overscan,
    horizontal,
    gap,
    paddingStart,
    scrollPaddingStart,
    rangeExtractor,
    ...(getItemKey ? { getItemKey } : {}),
  });
  const totalSize = v.getTotalSize();
  const items = v.getVirtualItems().map(({ index, start, size }) => ({ index, start, size }));
  const measure = useCallback(
    (el: HTMLElement | null) => {
      if (el) v.measureElement(el);
    },
    [v],
  );
  const scrollToIndex = useCallback(
    (i: number, align: 'start' | 'auto' = 'start') => v.scrollToIndex(i, { align }),
    [v],
  );
  const place = useCallback((el: HTMLElement | null, item: VirtualItem) => {
    if (!el) return;
    setCssPx(el, '--wf-virtual-start', item.start);
    setCssPx(el, '--wf-virtual-size', item.size);
  }, []);
  const placeTotal = useCallback(
    (el: HTMLElement | null) => {
      if (el) setCssPx(el, '--wf-virtual-total', totalSize);
    },
    [totalSize],
  );
  const first = items[0];
  const last = items.at(-1);
  const before = first ? first.start : 0;
  const after = last ? Math.max(0, totalSize - last.start - last.size) : 0;
  const placeGap = useCallback(
    (el: HTMLElement | null, edge: 'before' | 'after') => {
      if (el) setCssPx(el, '--wf-virtual-gap', edge === 'before' ? before : after);
    },
    [before, after],
  );
  return { items, totalSize, scrollToIndex, measure, place, placeTotal, placeGap };
}
