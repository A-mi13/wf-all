import { act, render, screen } from '@testing-library/react';
import { StrictMode, useRef } from 'react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { useVirtualRows, VIRTUAL_OVERSCAN } from './useVirtualRows';
import { VirtualList } from './VirtualList';

// Виртуализация (спека UI-фундамента §4.5, стр. 573–588): DOM держит видимые строки ± запас,
// положение — только числами --wf-virtual-* через setCssPx. jsdom не раскладывает: окно прокрутки —
// 400 px (clientHeight у [data-virtual-viewport]), строка — 40 px (offsetHeight у [data-index]).
const VIEWPORT = 400;
const ROW = 40;

function stubLayout() {
  const proto = HTMLElement.prototype;
  const client = Object.getOwnPropertyDescriptor(proto, 'clientHeight');
  const offset = Object.getOwnPropertyDescriptor(proto, 'offsetHeight');
  const scrollTo = proto.scrollTo;
  const top = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
  const tops = new WeakMap<Element, number>();
  // jsdom не хранит scrollTop — держим сами
  Object.defineProperty(proto, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return tops.get(this) ?? 0;
    },
    set(this: HTMLElement, v: number) {
      tops.set(this, v);
    },
  });
  Object.defineProperty(proto, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.hasAttribute('data-virtual-viewport') ? VIEWPORT : 0;
    },
  });
  const scrollHeight = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollHeight');
  // полная длина окна — та, что список записал в --wf-virtual-total (иначе прокрутка упрётся в 0)
  Object.defineProperty(proto, 'scrollHeight', {
    configurable: true,
    get(this: HTMLElement) {
      const inner = this.firstElementChild as HTMLElement | null;
      return parseFloat(inner?.style.getPropertyValue('--wf-virtual-total') ?? '') || 0;
    },
  });
  Object.defineProperty(proto, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return this.hasAttribute('data-index') ? ROW : 0;
    },
  });
  // прокрутка элемента: jsdom её не умеет — ставим scrollTop и шлём scroll, как браузер
  proto.scrollTo = function (this: HTMLElement, opts?: ScrollToOptions | number) {
    this.scrollTop = typeof opts === 'object' ? (opts.top ?? 0) : 0;
    this.dispatchEvent(new Event('scroll'));
  } as typeof proto.scrollTo;
  return () => {
    if (client) Object.defineProperty(proto, 'clientHeight', client);
    if (offset) Object.defineProperty(proto, 'offsetHeight', offset);
    proto.scrollTo = scrollTo;
    delete (proto as { scrollTop?: number }).scrollTop;
    delete (proto as { scrollHeight?: number }).scrollHeight;
    if (scrollHeight) Object.defineProperty(Element.prototype, 'scrollHeight', scrollHeight);
    if (top) Object.defineProperty(Element.prototype, 'scrollTop', top);
  };
}

let restore: () => void;
beforeEach(() => {
  restore = stubLayout();
});
afterEach(() => {
  restore();
  vi.restoreAllMocks();
});

const ITEMS = Array.from({ length: 1000 }, (_, i) => `Элемент ${i}`);

function List({ jump }: { jump?: (fn: (i: number) => void) => void }) {
  return (
    <VirtualList
      label="Тысяча"
      items={ITEMS}
      estimate={ROW}
      renderItem={(item) => <span>{item}</span>}
      onReady={jump}
    />
  );
}

describe('VirtualList', () => {
  test('1000 элементов → в DOM только видимые и запас', () => {
    renderWithIntl(<List />);
    const list = screen.getByRole('list', { name: 'Тысяча' });
    const rendered = screen.getAllByRole('listitem');
    // 400 / 40 = 10 видимых, запас по умолчанию — 5 (ниже; сверху нечего)
    expect(rendered.length).toBeGreaterThan(0);
    expect(rendered.length).toBeLessThanOrEqual(10 + 2 * 5);
    expect(list).toHaveTextContent('Элемент 0');
    expect(list).not.toHaveTextContent('Элемент 100');
    // читалка знает размер и позицию
    expect(rendered[0]).toHaveAttribute('aria-setsize', '1000');
    expect(rendered[0]).toHaveAttribute('aria-posinset', '1');
  });

  test('положение и полная высота — переменными --wf-virtual-*, без inline-стилей', () => {
    renderWithIntl(<List />);
    const items = screen.getAllByRole('listitem');
    const second = items[1];
    expect(second?.style.getPropertyValue('--wf-virtual-start')).toBe(`${ROW}px`);
    const spacer = screen.getByRole('list', { name: 'Тысяча' });
    expect(spacer.style.getPropertyValue('--wf-virtual-total')).toBe(`${1000 * ROW}px`);
    // кроме переменных --wf-* ничего не записано
    for (const el of [spacer, ...items]) {
      for (let i = 0; i < el.style.length; i++) expect(el.style.item(i)).toMatch(/^--wf-virtual-/);
    }
  });

  test('scrollToIndex(900) → элемент 900 в DOM', async () => {
    let go: ((i: number) => void) | undefined;
    renderWithIntl(<List jump={(fn) => (go = fn)} />);
    expect(screen.queryByText('Элемент 900')).toBeNull();
    act(() => go?.(900));
    expect(await screen.findByText('Элемент 900')).toBeInTheDocument();
    expect(screen.queryByText('Элемент 0')).toBeNull();
  });

  test('StrictMode: слушатели прокрутки сняты после размонтирования', () => {
    const added = vi.spyOn(HTMLElement.prototype, 'addEventListener');
    const removed = vi.spyOn(HTMLElement.prototype, 'removeEventListener');
    const { unmount } = render(
      <StrictMode>
        <Hooked />
      </StrictMode>,
    );
    unmount();
    // только окно прокрутки: React вешает свои scroll/scrollend на корневой контейнер навсегда
    const scrolls = (spy: typeof added) =>
      spy.mock.calls.filter(
        ([type], i) =>
          (type === 'scroll' || type === 'scrollend') &&
          (spy.mock.contexts[i] as HTMLElement).hasAttribute('data-virtual-viewport'),
      ).length;
    expect(scrolls(added)).toBeGreaterThan(0);
    expect(scrolls(removed)).toBe(scrolls(added));
  });
});

// Хук напрямую: таблица (волна 2) ставит строки сама.
function Hooked() {
  const ref = useRef<HTMLDivElement>(null);
  const { items } = useVirtualRows({ count: 100, estimate: ROW, scrollRef: ref });
  return (
    <div ref={ref} data-virtual-viewport="">
      {items.map((it) => (
        <div key={it.index} data-index={it.index}>
          {it.index}
        </div>
      ))}
    </div>
  );
}

describe('useVirtualRows', () => {
  test('горизонтально — окно по ширине (матрица флагов)', () => {
    // ширина окна 1000 px при высоте 400: столбцов по 100 px видно 10 (+ запас), а не 4 по высоте
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return this.hasAttribute('data-virtual-viewport') ? 1000 : 0;
    });
    function H() {
      const ref = useRef<HTMLDivElement>(null);
      const { totalSize, items } = useVirtualRows({
        count: 2000,
        estimate: 100,
        scrollRef: ref,
        horizontal: true,
      });
      return (
        <div ref={ref} data-virtual-viewport="">
          <output>{totalSize}</output>
          <span data-testid="count">{items.length}</span>
        </div>
      );
    }
    render(<H />);
    expect(screen.getByRole('status')).toHaveTextContent('200000');
    expect(screen.getByTestId('count')).toHaveTextContent(String(10 + VIRTUAL_OVERSCAN));
  });
});
