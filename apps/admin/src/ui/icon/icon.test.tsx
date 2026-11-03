import { ICONS, OPTICAL } from '@wf/icons';
import { render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { Icon } from './Icon';
import styles from './Icon.module.css';
import { Illustration } from './Illustration';

// Иконки админки из @wf/icons (спека UI-фундамента §4.9, план UF1a.3, U5, U7, U25).

// Прежний NAV_ICONS.log (src/ui/shell/icons.ts до переноса, контур из макета 08) — меню не меняется.
const OLD_LOG =
  'M5 5 H12 A3 3 0 0 1 15 8 V20 A2.4 2.4 0 0 0 12.6 18 H5 Z M19 5 H15.4 A2.4 2.4 0 0 0 13 7.4';

const paths = (c: HTMLElement) => [...c.querySelectorAll('path')];

test('без label — декоративная: aria-hidden, не в дереве доступности, без фокуса', () => {
  const { container } = render(<Icon name="ball" />);
  const svg = container.querySelector('svg')!;
  expect(svg).toHaveAttribute('aria-hidden', 'true');
  expect(svg).toHaveAttribute('focusable', 'false');
  expect(screen.queryByRole('img')).toBeNull();
});

test('с label — role="img" и имя', () => {
  render(<Icon name="ball" label="Мяч" />);
  expect(screen.getByRole('img', { name: 'Мяч' })).toBeInTheDocument();
});

test('слой stroke — fill="none", толщина атрибутом; слой fill — без stroke-width', () => {
  const { container } = render(<Icon name="ball" size={24} />);
  const layers = ICONS.ball.layers;
  const els = paths(container);
  expect(els).toHaveLength(layers.length);
  els.forEach((el, i) => {
    const layer = layers[i]!;
    expect(el).toHaveAttribute('d', layer.d);
    if (layer.paint === 'stroke') {
      expect(el).toHaveAttribute('fill', 'none');
      expect(el).toHaveAttribute('stroke-width');
    } else {
      expect(el).not.toHaveAttribute('stroke-width');
    }
  });
  expect(container.querySelector('svg')).toHaveAttribute('viewBox', ICONS.ball.viewBox);
  expect(container.querySelector('svg')).not.toHaveAttribute('style');
});

test('Icon name="log" рисует прежний контур меню, толщина 2.2 на 20 и 24 — без OPTICAL', () => {
  for (const size of [20, 24] as const) {
    const { container, unmount } = render(<Icon name="log" size={size} />);
    const [el, ...rest] = paths(container);
    expect(rest).toEqual([]);
    expect(el).toHaveAttribute('d', OLD_LOG);
    expect(el).toHaveAttribute('stroke-width', '2.2');
    expect(container.querySelector('svg')).toHaveAttribute('width', String(size));
    unmount();
  }
});

test('иконка артборда на 20 px — толщина слоя × 1.25 (OPTICAL), на 16 — × OPTICAL[16]', () => {
  expect(OPTICAL[20]).toBe(1.25);
  const first = ICONS.ball.layers[0]!;
  expect(first.width).toBe(2.4);
  const at = (size: 16 | 20) => paths(render(<Icon name="ball" size={size} />).container)[0]!;
  expect(at(20)).toHaveAttribute('stroke-width', '3');
  expect(at(16)).toHaveAttribute('stroke-width', String(Math.round(2.4 * OPTICAL[16] * 100) / 100));
});

test('цвет — классами ролей: knockout — поверхность, на акценте — text-inverse; opacity и cap — атрибутами', () => {
  const { container } = render(<Icon name="filter" />);
  const els = paths(container);
  const layers = ICONS.filter.layers;
  const surface = layers.findIndex((l) => l.role === 'knockout' && l.on === 'surface');
  const onAccent = layers.findIndex((l) => l.role === 'knockout' && l.on === 'accent');
  expect(els[surface]).toHaveClass(styles.knockout!);
  expect(els[surface]).not.toHaveClass(styles.onAccent!);
  expect(els[onAccent]).toHaveClass(styles.onAccent!);
  expect(els[layers.findIndex((l) => l.role === 'object')]).toHaveClass(styles.object!);
  expect(els[layers.findIndex((l) => l.role === 'accent')]).toHaveClass(styles.accent!);

  const pitch = render(<Icon name="pitch" />).container;
  const i = ICONS.pitch.layers.findIndex((l) => l.cap === 'butt');
  expect(paths(pitch)[i]).toHaveAttribute('stroke-linecap', 'butt');
  expect(paths(pitch)[i]).toHaveAttribute('opacity', String(ICONS.pitch.layers[i]!.opacity));
  expect(paths(pitch)[i]).toHaveClass(styles.detail!);
});

test('цвет не атрибутом: ни у одного слоя нет stroke/fill с цветом', () => {
  const { container } = render(<Icon name="rent" />);
  for (const el of paths(container)) {
    expect(el.getAttribute('stroke')).toBeNull();
    expect(['none', null]).toContain(el.getAttribute('fill'));
  }
});

test('Illustration — всегда декоративная, предмет — свой класс (text-muted), толщина без множителя', () => {
  const { container } = render(<Illustration name="ball" size={64} />);
  const svg = container.querySelector('svg')!;
  expect(svg).toHaveAttribute('aria-hidden', 'true');
  expect(svg).toHaveAttribute('width', '64');
  const [first] = paths(container);
  expect(first).toHaveClass(styles.illustrationObject!);
  expect(first).toHaveAttribute('stroke-width', '2.4');
});
