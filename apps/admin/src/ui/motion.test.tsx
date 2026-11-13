import { act, renderHook } from '@testing-library/react';
import { expect, test } from 'vitest';
import { stubMedia } from '../test/media';
import { useReducedMotion } from './motion';

// «Уменьшить движение» (спека UI-фундамента §2.7, §4.15; план UF2b.9): JS-анимации и переходы спрашивают
// useReducedMotion() — matchMedia('(prefers-reduced-motion: reduce)') с подпиской.

test('настройка включена — true', () => {
  stubMedia({ width: 1440, pointer: 'fine', reducedMotion: true });
  expect(renderHook(() => useReducedMotion()).result.current).toBe(true);
});

test('смена настройки на лету — обновление', () => {
  const media = stubMedia({ width: 1440, pointer: 'fine' });
  const { result } = renderHook(() => useReducedMotion());
  expect(result.current).toBe(false);
  act(() => media.set({ width: 1440, pointer: 'fine', reducedMotion: true }));
  expect(result.current).toBe(true);
  act(() => media.set({ width: 1440, pointer: 'fine', reducedMotion: false }));
  expect(result.current).toBe(false);
});

test('без matchMedia (старый движок) — движение не уменьшено', () => {
  expect(renderHook(() => useReducedMotion()).result.current).toBe(false);
});
