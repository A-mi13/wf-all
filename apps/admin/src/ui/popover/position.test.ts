import { describe, expect, test } from 'vitest';
import { GAP, MARGIN, place } from './position';

// Положение пузыря от якоря числами (спека UI-фундамента §4.8, N3): чистая функция — сторона по
// placement, переворот, если не помещается; выравнивание по концу у правого края; maxHeight — не больше
// места на выбранной стороне.
const rect = (left: number, top: number, width: number, height: number) => ({
  left,
  top,
  width,
  height,
  right: left + width,
  bottom: top + height,
});
const viewport = { width: 1000, height: 800 };

describe('place', () => {
  test('снизу помещается — снизу от якоря, по началу', () => {
    const p = place(rect(100, 100, 120, 40), { width: 200, height: 150 }, viewport, 'bottom-start');
    expect(p).toEqual({
      x: 100,
      y: 140 + GAP,
      maxHeight: 800 - 140 - GAP - MARGIN,
      side: 'bottom',
    });
  });

  test('снизу не помещается, сверху места больше — сверху', () => {
    const p = place(rect(100, 600, 120, 40), { width: 200, height: 300 }, viewport, 'bottom-start');
    expect(p.side).toBe('top');
    expect(p.maxHeight).toBe(600 - GAP - MARGIN);
    expect(p.y).toBe(600 - GAP - 300);
  });

  test('сверху не помещается — снизу (top-start переворачивается)', () => {
    const p = place(rect(100, 30, 120, 40), { width: 200, height: 100 }, viewport, 'top-start');
    expect(p.side).toBe('bottom');
    expect(p.y).toBe(70 + GAP);
  });

  test('у правого края — выравнивание по концу якоря', () => {
    const p = place(rect(900, 100, 80, 40), { width: 240, height: 100 }, viewport, 'bottom-start');
    expect(p.x).toBe(980 - 240);
  });

  test('bottom-end — по концу якоря; у левого края — по началу якоря', () => {
    expect(
      place(rect(300, 100, 80, 40), { width: 200, height: 50 }, viewport, 'bottom-end').x,
    ).toBe(180);
    expect(place(rect(10, 100, 40, 40), { width: 200, height: 50 }, viewport, 'bottom-end').x).toBe(
      10,
    );
    // якорь у самого края — не левее отступа окна
    expect(place(rect(0, 100, 4, 40), { width: 200, height: 50 }, viewport, 'bottom-end').x).toBe(
      MARGIN,
    );
  });

  test('maxHeight не больше места: высокий пузырь режется местом стороны', () => {
    const p = place(
      rect(100, 380, 120, 40),
      { width: 200, height: 2000 },
      viewport,
      'bottom-start',
    );
    const below = 800 - 420 - GAP - MARGIN;
    const above = 380 - GAP - MARGIN;
    expect(p.maxHeight).toBe(Math.max(below, above));
    expect(p.maxHeight).toBeLessThanOrEqual(800);
    // пузырь с maxHeight целиком в окне
    expect(p.y).toBeGreaterThanOrEqual(MARGIN);
    expect(p.y + p.maxHeight).toBeLessThanOrEqual(800 - MARGIN);
  });

  test('right — справа; у края — переворот влево', () => {
    expect(
      place(rect(100, 100, 40, 40), { width: 200, height: 50 }, viewport, 'right'),
    ).toMatchObject({ x: 140 + GAP, y: 100, side: 'right' });
    expect(
      place(rect(900, 100, 40, 40), { width: 200, height: 50 }, viewport, 'right'),
    ).toMatchObject({ x: 900 - GAP - 200, side: 'left' });
  });

  // Ревью UF2a/I, m1: якорь меню ниже окна, список высокий — maxHeight не больше окна, весь список
  // достижим прокруткой внутри пузыря.
  test('якорь ниже окна, высокий список — пузырь целиком в окне', () => {
    const p = place(
      rect(100, 900, 120, 40),
      { width: 200, height: 2000 },
      viewport,
      'bottom-start',
    );
    expect(p.maxHeight).toBeLessThanOrEqual(800 - 2 * MARGIN);
    expect(p.y).toBeGreaterThanOrEqual(MARGIN);
    expect(p.y + p.maxHeight).toBeLessThanOrEqual(800 - MARGIN);
  });

  test('якорь ушёл за верх окна — пузырь прижат к краю, а не за окном', () => {
    const p = place(
      rect(100, -200, 120, 40),
      { width: 200, height: 100 },
      viewport,
      'bottom-start',
    );
    expect(p.y).toBe(MARGIN);
  });
});
