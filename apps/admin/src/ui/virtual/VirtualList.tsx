import { type ReactNode, useEffect, useRef } from 'react';
import { useVirtualRows } from './useVirtualRows';
import styles from './Virtual.module.css';

// Длинный простой список (спека UI-фундамента §4.5): там, где content-visibility не годится — тысячи
// элементов, переход к элементу (scrollToIndex). Элементы разной высоты меряются по факту. Окно
// прокрутки заполняет высоту родителя. Читалка знает размер и позицию: aria-setsize/aria-posinset.
// onReady — отдаёт scrollToIndex вызывателю (переход к найденному элементу).
export function VirtualList<T>({
  items,
  estimate,
  renderItem,
  label,
  onReady,
}: {
  items: readonly T[];
  estimate: number;
  renderItem: (item: T, index: number) => ReactNode;
  label: string;
  onReady?: (scrollToIndex: (i: number) => void) => void;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const {
    items: visible,
    scrollToIndex,
    measure,
    place,
    placeTotal,
  } = useVirtualRows({
    count: items.length,
    estimate,
    scrollRef: viewport,
  });
  useEffect(() => onReady?.(scrollToIndex), [onReady, scrollToIndex]);
  return (
    <div ref={viewport} className={styles.viewport} data-virtual-viewport="">
      <ul ref={placeTotal} className={styles.list} aria-label={label}>
        {visible.map((it) => (
          <li
            key={it.index}
            data-index={it.index}
            className={styles.item}
            aria-setsize={items.length}
            aria-posinset={it.index + 1}
            ref={(el) => {
              place(el, it);
              measure(el);
            }}
          >
            {renderItem(items[it.index] as T, it.index)}
          </li>
        ))}
      </ul>
    </div>
  );
}
