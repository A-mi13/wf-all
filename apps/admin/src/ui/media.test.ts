import { act, renderHook } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { stubMedia } from '../test/media';
import { useIsCompact, useIsHandheld, useIsMobile } from './media';

// Ширина решает раскладку, устройство — «только с компьютера» (спека UI-фундамента §3.2):
// useIsHandheld = палец И экран уже breakpoint.compact; масштаб на ноутбуке (640 + мышь) — не телефон.
const useAll = () => ({
  mobile: useIsMobile(),
  compact: useIsCompact(),
  handheld: useIsHandheld(),
});

describe('медиа-хуки', () => {
  test('без matchMedia — широкий компьютер', () => {
    const { result } = renderHook(useAll);
    expect(result.current).toEqual({ mobile: false, compact: false, handheld: false });
  });

  test.each([
    [390, 'coarse', { mobile: true, compact: true, handheld: true }],
    [640, 'fine', { mobile: true, compact: true, handheld: false }],
    [900, 'fine', { mobile: false, compact: true, handheld: false }],
    [900, 'coarse', { mobile: false, compact: true, handheld: true }],
    [1100, 'coarse', { mobile: false, compact: false, handheld: false }],
    [1280, 'fine', { mobile: false, compact: false, handheld: false }],
  ] as const)('%i px + %s', (width, pointer, expected) => {
    stubMedia({ width, pointer });
    const { result } = renderHook(useAll);
    expect(result.current).toEqual(expected);
  });

  test('границы — по токенам: 767 узко, 768 нет; 1023 компактно, 1024 нет', () => {
    const media = stubMedia({ width: 767, pointer: 'coarse' });
    const { result } = renderHook(useAll);
    expect(result.current.mobile).toBe(true);
    act(() => media.set({ width: 768, pointer: 'coarse' }));
    expect(result.current).toEqual({ mobile: false, compact: true, handheld: true });
    act(() => media.set({ width: 1023, pointer: 'coarse' }));
    expect(result.current.handheld).toBe(true);
    act(() => media.set({ width: 1024, pointer: 'coarse' }));
    expect(result.current).toEqual({ mobile: false, compact: false, handheld: false });
  });

  test('устройство меняется на лету — хуки следом (смена указателя при той же ширине)', () => {
    const media = stubMedia({ width: 390, pointer: 'fine' });
    const { result } = renderHook(useAll);
    expect(result.current.handheld).toBe(false);
    act(() => media.set({ width: 390, pointer: 'coarse' }));
    expect(result.current.handheld).toBe(true);
  });
});
