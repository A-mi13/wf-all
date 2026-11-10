import { tokens } from '@wf/tokens';

// Положение пузыря от якоря — чистая функция (спека UI-фундамента §4.8, N3): сторона по placement,
// переворот на противоположную, если там места больше и на своей пузырь не помещается; по оси
// выравнивания — начало или конец якоря, у края окна — выравнивание по другому концу и прижим к
// отступу. Координаты — от окна (пузырь в верхнем слое, position: fixed). Якорь ушёл за окно —
// пузырь прижат к краю (меню и список остаются видимыми; подсказку закрывает Popover).
export type Placement = 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end' | 'right' | 'left';
export type Side = 'bottom' | 'top' | 'right' | 'left';
export type AnchorRect = Pick<DOMRect, 'left' | 'top' | 'right' | 'bottom' | 'width' | 'height'>;
export type Size = { width: number; height: number };
export type Position = { x: number; y: number; maxHeight: number; side: Side };

export const GAP = tokens.space[1]; // между якорем и пузырём
export const MARGIN = tokens.space[2]; // от края окна

const clamp = (v: number, min: number, max: number) =>
  Math.max(min, Math.min(v, Math.max(min, max)));

export function place(
  anchor: AnchorRect,
  size: Size,
  viewport: Size,
  placement: Placement,
): Position {
  const vertical = !(placement === 'right' || placement === 'left');
  if (vertical) {
    const below = viewport.height - anchor.bottom - GAP - MARGIN;
    const above = anchor.top - GAP - MARGIN;
    const preferBottom = placement.startsWith('bottom');
    const [own, other] = preferBottom ? [below, above] : [above, below];
    const flip = size.height > own && other > own;
    const side: Side = preferBottom !== flip ? 'bottom' : 'top';
    // не больше окна: якорь ушёл ниже или выше окна — весь список достижим прокруткой (ревью m1)
    const maxHeight = Math.min(
      Math.max(0, side === 'bottom' ? below : above),
      Math.max(0, viewport.height - 2 * MARGIN),
    );
    const h = Math.min(size.height, maxHeight);
    const rawY = side === 'bottom' ? anchor.bottom + GAP : anchor.top - GAP - h;
    const y = clamp(rawY, MARGIN, viewport.height - MARGIN - h);
    const end = placement.endsWith('end');
    let x = end ? anchor.right - size.width : anchor.left;
    // у края — по другому концу якоря
    if (!end && x + size.width > viewport.width - MARGIN) x = anchor.right - size.width;
    if (end && x < MARGIN) x = anchor.left;
    x = clamp(x, MARGIN, viewport.width - MARGIN - size.width);
    return { x, y, maxHeight, side };
  }
  const right = viewport.width - anchor.right - GAP - MARGIN;
  const left = anchor.left - GAP - MARGIN;
  const preferRight = placement === 'right';
  const [own, other] = preferRight ? [right, left] : [left, right];
  const flip = size.width > own && other > own;
  const side: Side = preferRight !== flip ? 'right' : 'left';
  const rawX = side === 'right' ? anchor.right + GAP : anchor.left - GAP - size.width;
  const x = clamp(rawX, MARGIN, viewport.width - MARGIN - size.width);
  const maxHeight = Math.max(0, viewport.height - 2 * MARGIN);
  const y = clamp(anchor.top, MARGIN, viewport.height - MARGIN - Math.min(size.height, maxHeight));
  return { x, y, maxHeight, side };
}
