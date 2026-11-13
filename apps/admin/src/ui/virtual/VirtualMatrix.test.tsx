import { act, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Switch } from '../Switch';
import { MATRIX_VIRTUALIZE_FROM } from './matrix';
import { VirtualMatrix } from './VirtualMatrix';

// Матрица с горизонтальной виртуализацией (спека UI-фундамента §4.5, флаги функций — Task 22): закреплённый
// первый столбец, неподвижные столбцы («Везде») и столбцы-города окном. jsdom не раскладывает: ширина окна
// прокрутки ([data-matrix-scroll]) — 600 px, scrollLeft хранится здесь, scrollTo ставит его и шлёт scroll.
const VIEWPORT = 600;

function stubHorizontal() {
  const proto = HTMLElement.prototype;
  const saved = (['clientWidth', 'clientHeight', 'scrollLeft', 'scrollWidth'] as const).map(
    (name) => [name, Object.getOwnPropertyDescriptor(proto, name)] as const,
  );
  const scrollTo = proto.scrollTo;
  const lefts = new WeakMap<Element, number>();
  const isViewport = (el: Element) => el.hasAttribute('data-matrix-scroll');
  Object.defineProperty(proto, 'scrollLeft', {
    configurable: true,
    get(this: HTMLElement) {
      return lefts.get(this) ?? 0;
    },
    set(this: HTMLElement, v: number) {
      lefts.set(this, v);
    },
  });
  Object.defineProperty(proto, 'clientWidth', {
    configurable: true,
    get(this: HTMLElement) {
      return isViewport(this) ? VIEWPORT : 0;
    },
  });
  // полная ширина — с запасом: виртуализация не считает прокрутку за край ложной
  Object.defineProperty(proto, 'scrollWidth', {
    configurable: true,
    get(this: HTMLElement) {
      return isViewport(this) ? 1_000_000 : 0;
    },
  });
  Object.defineProperty(proto, 'clientHeight', {
    configurable: true,
    get(this: HTMLElement) {
      return isViewport(this) ? 300 : 0;
    },
  });
  proto.scrollTo = function (this: HTMLElement, opts?: ScrollToOptions | number) {
    this.scrollLeft = typeof opts === 'object' ? (opts.left ?? 0) : 0;
    this.dispatchEvent(new Event('scroll'));
  } as typeof proto.scrollTo;
  return () => {
    for (const [name, desc] of saved) {
      if (desc) Object.defineProperty(proto, name, desc);
      else delete (proto as unknown as Record<string, unknown>)[name];
    }
    proto.scrollTo = scrollTo;
  };
}

let restore: () => void;
beforeEach(() => {
  restore = stubHorizontal();
});
afterEach(() => restore());

type Row = { id: string; name: string };
const ROWS: Row[] = [
  { id: 'a', name: 'Прогнозы' },
  { id: 'b', name: 'Турниры' },
];
const cities = (n: number) => Array.from({ length: n }, (_, i) => `Город ${i + 1}`);

function Matrix({ columns }: { columns: string[] }) {
  return (
    <VirtualMatrix
      label="Флаги функций"
      rows={ROWS}
      rowKey={(r) => r.id}
      corner="Функция"
      rowHeader={(r) => r.name}
      fixed={[
        {
          id: 'all',
          header: 'Везде',
          cell: (r) => (
            <Switch checked={false} onChange={() => {}} label={`${r.name} — везде`} hideLabel />
          ),
        },
      ]}
      columns={columns}
      columnKey={(c) => c}
      columnHeader={(c) => c}
      cell={(r, c) => (
        <Switch checked={false} onChange={() => {}} label={`${r.name} — ${c}`} hideLabel />
      )}
    />
  );
}

const renderedCities = () =>
  screen.getAllByRole('columnheader').filter((h) => h.textContent?.startsWith('Город'));

describe('VirtualMatrix', () => {
  test('меньше порога — все столбцы в DOM; aria-colcount и aria-colindex по порядку', () => {
    renderWithIntl(<Matrix columns={cities(3)} />);
    const table = screen.getByRole('table', { name: 'Флаги функций' });
    expect(table).toHaveAttribute('aria-colcount', '5');
    const heads = screen.getAllByRole('columnheader');
    expect(heads.map((h) => h.getAttribute('aria-colindex'))).toEqual(['1', '2', '3', '4', '5']);
    const row = screen.getAllByRole('row')[1];
    expect(row).toBeDefined();
    const cells = [...(row as HTMLElement).querySelectorAll('[aria-colindex]')];
    expect(cells.map((c) => c.getAttribute('aria-colindex'))).toEqual(['1', '2', '3', '4', '5']);
    expect(screen.getByRole('rowheader', { name: 'Прогнозы' })).toBeInTheDocument();
  });

  test('2000 городов — в DOM окно столбцов, aria-colcount — все; у ячейки — её номер', () => {
    renderWithIntl(<Matrix columns={cities(2000)} />);
    expect(screen.getByRole('table')).toHaveAttribute('aria-colcount', '2002');
    const shown = renderedCities();
    expect(shown.length).toBeGreaterThan(0);
    expect(shown.length).toBeLessThan(20);
    expect(shown[0]).toHaveAttribute('aria-colindex', '3');
    // ячейка того же города в строке — тот же номер столбца
    const cell = screen.getByRole('switch', { name: 'Турниры — Город 2' }).closest('[role="cell"]');
    expect(cell).toHaveAttribute('aria-colindex', '4');
  });

  test(`порог виртуализации — ${MATRIX_VIRTUALIZE_FROM} столбцов`, () => {
    renderWithIntl(<Matrix columns={cities(MATRIX_VIRTUALIZE_FROM)} />);
    expect(renderedCities()).toHaveLength(MATRIX_VIRTUALIZE_FROM);
  });

  test('фокус держит свой столбец в DOM: прокрутка мимо не роняет фокус на body', async () => {
    renderWithIntl(<Matrix columns={cities(2000)} />);
    const sw = screen.getByRole('switch', { name: 'Прогнозы — Город 4' });
    act(() => sw.focus());
    const scroll = document.querySelector<HTMLElement>('[data-matrix-scroll]');
    act(() => scroll?.scrollTo({ left: 200_000 }));
    // окно ушло далеко: соседа нет, столбец с фокусом остался
    expect(screen.queryByRole('switch', { name: 'Прогнозы — Город 5' })).toBeNull();
    expect(sw).toBeInTheDocument();
    expect(document.activeElement).toBe(sw);
  });

  test('Tab с последнего отрисованного столбца — на переключатель следующего города', async () => {
    const { user } = renderWithIntl(<Matrix columns={cities(2000)} />);
    const last = renderedCities().at(-1);
    const name = last?.textContent ?? '';
    const n = Number(name.replace('Город ', ''));
    const sw = screen.getByRole('switch', { name: `Прогнозы — ${name}` });
    act(() => sw.focus());
    await user.tab();
    expect(document.activeElement).toBe(
      screen.getByRole('switch', { name: `Прогнозы — Город ${n + 1}` }),
    );
  });
});
