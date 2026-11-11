import { useVirtualizer } from '@tanstack/react-virtual';
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
  scrollToIndex(i: number): void;
  // ref-колбэк элемента с data-index: меряет фактический размер (строки разной высоты)
  measure(el: HTMLElement | null): void;
  // ставит элементу его положение и размер (переменные --wf-virtual-start/-size)
  place(el: HTMLElement | null, item: VirtualItem): void;
  // ставит контейнеру полную длину (--wf-virtual-total)
  placeTotal(el: HTMLElement | null): void;
};

// Запас по умолчанию: строки за краем окна, чтобы быстрая прокрутка не показывала пустоту.
export const VIRTUAL_OVERSCAN = 5;

export function useVirtualRows({
  count,
  estimate,
  scrollRef,
  overscan = VIRTUAL_OVERSCAN,
  horizontal = false,
}: {
  count: number;
  estimate: number;
  scrollRef: RefObject<HTMLElement | null>;
  overscan?: number;
  horizontal?: boolean;
}): VirtualRows {
  const v = useVirtualizer({
    count,
    estimateSize: () => estimate,
    getScrollElement: () => scrollRef.current,
    overscan,
    horizontal,
  });
  const totalSize = v.getTotalSize();
  const items = v.getVirtualItems().map(({ index, start, size }) => ({ index, start, size }));
  const measure = useCallback(
    (el: HTMLElement | null) => {
      if (el) v.measureElement(el);
    },
    [v],
  );
  const scrollToIndex = useCallback((i: number) => v.scrollToIndex(i, { align: 'start' }), [v]);
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
  return { items, totalSize, scrollToIndex, measure, place, placeTotal };
}
