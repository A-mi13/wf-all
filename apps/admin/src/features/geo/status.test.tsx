import { renderHook } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { providers } from '../../test/render';
import { cityStatusTone, useCityStatus } from './status';

// Статус города → тон и подпись чипа (макет 07): живой — успех, пилот — внимание, ожидание —
// нейтральный. Enum открытый (сервер новее сборки): незнакомый — нейтральный «—», не сам код.

describe('статус города', () => {
  test('тон', () => {
    expect(cityStatusTone('live')).toBe('success');
    expect(cityStatusTone('pilot')).toBe('warning');
    expect(cityStatusTone('waitlist')).toBe('neutral');
    expect(cityStatusTone('archived')).toBe('neutral');
    expect(cityStatusTone('constructor')).toBe('neutral');
  });

  test('подпись из локалей; незнакомый — «—»', () => {
    const { result } = renderHook(() => useCityStatus(), { wrapper: providers() });
    expect(result.current('live')).toEqual({ tone: 'success', text: 'живой' });
    expect(result.current('pilot')).toEqual({ tone: 'warning', text: 'пилот' });
    expect(result.current('waitlist')).toEqual({ tone: 'neutral', text: 'ожидание' });
    expect(result.current('archived')).toEqual({ tone: 'neutral', text: '—' });
  });
});
