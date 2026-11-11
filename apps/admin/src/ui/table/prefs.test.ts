import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { stubMedia } from '../../test/media';
import {
  DENSITY_STORAGE_KEY,
  readColumns,
  TABLE_PREFS_PREFIX,
  useDensity,
  writeColumns,
} from './prefs';

const KNOWN = ['name', 'email', 'role'];

afterEach(() => {
  vi.restoreAllMocks();
  // плотность в памяти модуля (storage бросал) — снять рабочей записью, чтобы не унаследовал следующий тест
  const { result, unmount } = renderHook(() => useDensity());
  act(() => result.current[1]('normal'));
  unmount();
  localStorage.clear();
});

describe('readColumns / writeColumns', () => {
  test('ключ — wf.admin.table.<tableId>; записанное читается обратно', () => {
    writeColumns('staff', { hidden: ['email'], shown: ['role'] }, KNOWN);
    expect(JSON.parse(localStorage.getItem(`${TABLE_PREFS_PREFIX}staff`) ?? '')).toEqual({
      hidden: ['email'],
      shown: ['role'],
    });
    expect(readColumns('staff', KNOWN)).toEqual({ hidden: ['email'], shown: ['role'] });
    expect(readColumns('audit', KNOWN)).toEqual({ hidden: [], shown: [] });
  });

  test('незнакомый id столбца (старая версия) отброшен', () => {
    localStorage.setItem(
      'wf.admin.table.staff',
      JSON.stringify({ hidden: ['legacy', 'email'], shown: ['gone', 'role'] }),
    );
    expect(readColumns('staff', KNOWN)).toEqual({ hidden: ['email'], shown: ['role'] });
  });

  test.each([
    ['не JSON', '{oops'],
    ['не объект', '[1,2]'],
    ['поля не массивы', '{"hidden":"email","shown":null}'],
    ['элементы не строки', '{"hidden":[1,{"id":"email"}],"shown":[true]}'],
  ])('мусор в storage (%s) — по умолчанию', (_, raw) => {
    localStorage.setItem('wf.admin.table.staff', raw);
    expect(readColumns('staff', KNOWN)).toEqual({ hidden: [], shown: [] });
  });

  test('storage бросает — по умолчанию, запись не роняет', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota', 'QuotaExceededError');
    });
    expect(readColumns('staff', KNOWN)).toEqual({ hidden: [], shown: [] });
    expect(() => writeColumns('staff', { hidden: ['email'], shown: [] }, KNOWN)).not.toThrow();
  });
});

describe('useDensity', () => {
  test('по умолчанию normal; выбор пишется в wf.admin.density и виден всем таблицам', () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const a = renderHook(() => useDensity());
    const b = renderHook(() => useDensity());
    expect(a.result.current[0]).toBe('normal');
    act(() => a.result.current[1]('compact'));
    expect(localStorage.getItem(DENSITY_STORAGE_KEY)).toBe('compact');
    expect(a.result.current[0]).toBe('compact');
    expect(b.result.current[0]).toBe('compact');
  });

  test('другая вкладка сменила плотность (событие storage) — значение из storage', () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    const { result } = renderHook(() => useDensity());
    expect(result.current[0]).toBe('normal');
    localStorage.setItem(DENSITY_STORAGE_KEY, 'compact');
    act(() => {
      window.dispatchEvent(new StorageEvent('storage', { key: DENSITY_STORAGE_KEY }));
    });
    expect(result.current[0]).toBe('compact');
  });

  test('pointer: coarse — всегда normal, даже если выбрано compact', () => {
    localStorage.setItem(DENSITY_STORAGE_KEY, 'compact');
    stubMedia({ width: 900, pointer: 'coarse' });
    const { result } = renderHook(() => useDensity());
    expect(result.current[0]).toBe('normal');
  });

  test('незнакомое значение — normal; storage бросает — normal, выбор живёт до перезагрузки', () => {
    stubMedia({ width: 1440, pointer: 'fine' });
    localStorage.setItem(DENSITY_STORAGE_KEY, 'huge');
    const first = renderHook(() => useDensity());
    expect(first.result.current[0]).toBe('normal');
    first.unmount();

    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError');
    });
    const { result } = renderHook(() => useDensity());
    expect(result.current[0]).toBe('normal');
    act(() => result.current[1]('compact'));
    expect(result.current[0]).toBe('compact');
  });
});
