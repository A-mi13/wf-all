import { describe, expect, test } from 'vitest';
import { diffKeys, withFallback } from './index';

const ru = { health: { ok: 'API работает', down: 'API недоступен' }, title: 'Админка' };

describe('withFallback', () => {
  test('пустая строка в en показывает ru', () => {
    const en = { health: { ok: '', down: 'API is down' }, title: '' };
    expect(withFallback(ru, en)).toEqual({
      health: { ok: 'API работает', down: 'API is down' },
      title: 'Админка',
    });
  });
  test('недостающий ключ берётся из ru', () => {
    expect(withFallback(ru, { health: { ok: 'OK' } })).toEqual({
      health: { ok: 'OK', down: 'API недоступен' },
      title: 'Админка',
    });
  });
  test('base не мутируется', () => {
    withFallback(ru, { title: 'Admin' });
    expect(ru.title).toBe('Админка');
  });
});

describe('diffKeys', () => {
  test('одинаковые наборы — пусто', () => {
    expect(diffKeys(ru, { health: { ok: '', down: '' }, title: '' })).toEqual({ missing: [], extra: [] });
  });
  test('находит отсутствующие и лишние пути', () => {
    expect(diffKeys(ru, { health: { ok: '' }, title: '', extra: { x: '' } })).toEqual({
      missing: ['health.down'],
      extra: ['extra.x'],
    });
  });
});
