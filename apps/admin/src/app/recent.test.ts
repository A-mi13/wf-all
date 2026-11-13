import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { meAdmin, meOwner } from '../test/me';
import { problem } from '../test/problem';
import { providers } from '../test/render';
import { http, server } from '../test/server';
import { useLogout } from './logout';
import {
  claimRecent,
  clearRecent,
  pushRecent,
  readRecent,
  RECENT_LIMIT,
  RECENT_PREFIX,
  type RecentItem,
} from './recent';
import { useSession } from './session';

// Недавнее палитры (спека UI-фундамента §4.1): localStorage wf.admin.recent.<staffId>, не больше 8, у сотрудника —
// только display_name. Общее устройство: claimRecent при входе стирает чужие ключи, clearRecent — при выходе.

const A = meOwner.id ?? 'a';
const B = meAdmin.id ?? 'b';
const city = (n: number): RecentItem => ({
  kind: 'city',
  id: `c${n}`,
  label: `Город ${n}`,
  path: `/geo/cities/c${n}`,
});

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe('recent', () => {
  test('новое — первым; 9-й вытесняет первый', () => {
    for (let n = 1; n <= 9; n++) pushRecent(A, city(n));
    const items = readRecent(A);
    expect(RECENT_LIMIT).toBe(8);
    expect(items.map((i) => i.id)).toEqual(['c9', 'c8', 'c7', 'c6', 'c5', 'c4', 'c3', 'c2']);
    // и в самом storage — не больше 8
    expect(JSON.parse(localStorage.getItem(RECENT_PREFIX + A) ?? '[]')).toHaveLength(8);
  });

  test('повтор поднимает запись наверх, без дубля', () => {
    pushRecent(A, city(1));
    pushRecent(A, city(2));
    pushRecent(A, { ...city(1), label: 'Новое имя' });
    expect(readRecent(A)).toEqual([{ ...city(1), label: 'Новое имя' }, city(2)]);
  });

  test('в записи нет почты и лишних полей — только kind, id, label, path', () => {
    const staff = {
      kind: 'staff',
      id: 's1',
      label: 'Ольга В.',
      path: '/staff?account=s1',
      email: 'olga.v@wf.example',
    } as RecentItem;
    pushRecent(A, staff);
    const raw = localStorage.getItem(RECENT_PREFIX + A) ?? '';
    expect(raw).not.toContain('@');
    expect(JSON.parse(raw)).toEqual([
      { kind: 'staff', id: 's1', label: 'Ольга В.', path: '/staff?account=s1' },
    ]);
  });

  test('мусор в storage — пусто; негодные записи и чужие адреса отброшены', () => {
    localStorage.setItem(RECENT_PREFIX + A, '{нет');
    expect(readRecent(A)).toEqual([]);
    localStorage.setItem(
      RECENT_PREFIX + A,
      JSON.stringify([
        city(1),
        { ...city(2), path: '//evil.example/x' },
        { ...city(3), path: 'https://evil.example' },
        { ...city(4), kind: 'unknown' },
        { kind: 'city', id: 5 },
        null,
      ]),
    );
    expect(readRecent(A)).toEqual([city(1)]);
  });

  test('claimRecent(B) стирает ключи A и не трогает своё и чужие настройки', () => {
    pushRecent(A, city(1));
    pushRecent(B, city(2));
    localStorage.setItem('wf.theme', 'dark');
    claimRecent(B);
    expect(localStorage.getItem(RECENT_PREFIX + A)).toBeNull();
    expect(readRecent(B)).toEqual([city(2)]);
    expect(localStorage.getItem('wf.theme')).toBe('dark');
  });

  test('clearRecent стирает все ключи недавнего', () => {
    pushRecent(A, city(1));
    pushRecent(B, city(2));
    localStorage.setItem('wf.theme', 'dark');
    clearRecent();
    expect(localStorage.getItem(RECENT_PREFIX + A)).toBeNull();
    expect(localStorage.getItem(RECENT_PREFIX + B)).toBeNull();
    expect(localStorage.getItem('wf.theme')).toBe('dark');
  });

  test('storage бросает (приватный режим) — пусто и без исключений', () => {
    const boom = () => {
      throw new DOMException('denied', 'SecurityError');
    };
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(boom);
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(boom);
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(boom);
    vi.spyOn(Storage.prototype, 'key').mockImplementation(boom);
    expect(readRecent(A)).toEqual([]);
    expect(() => pushRecent(A, city(1))).not.toThrow();
    expect(() => claimRecent(A)).not.toThrow();
    expect(() => clearRecent()).not.toThrow();
  });

  test('SessionProvider при входе сотрудника стирает недавнее другого', async () => {
    pushRecent(B, city(2));
    pushRecent(A, city(1));
    renderHook(() => useSession(), { wrapper: providers({ me: meOwner }) });
    await waitFor(() => expect(localStorage.getItem(RECENT_PREFIX + B)).toBeNull());
    expect(readRecent(A)).toEqual([city(1)]);
  });

  test('выход стирает недавнее (clearRecent в useLogout)', async () => {
    let signedOut = false;
    server.use(
      http.post('/v1/staff/auth/logout', ({ response }) => {
        signedOut = true;
        return response(204).empty();
      }),
      http.get('/v1/staff/me', ({ response }) =>
        signedOut
          ? response.untyped(problem(401, 'auth.unauthenticated'))
          : response(200).json(meOwner),
      ),
    );
    const { result } = renderHook(() => useLogout(), { wrapper: providers({ me: meOwner }) });
    pushRecent(A, city(1));
    act(() => result.current.mutate());
    await waitFor(() => expect(localStorage.getItem(RECENT_PREFIX + A)).toBeNull());
  });
});
