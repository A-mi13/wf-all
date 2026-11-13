import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { city } from '../../test/staff';
import { providers } from '../../test/render';
import { http, server } from '../../test/server';
import { CITIES_CAP, useAllCities } from './useAllCities';

type GeoCity = components['schemas']['GeoCity'];

// Все города для столбцов матрицы флагов (план B, Task 22; ревью планов minor 6): listGeoCities отдаёт до 100 за
// раз — хук проходит курсором до конца с потолком 2000; город флага, не попавший в загруженные, догружается по ids
// пачками по 100 (предел параметра ids контракта).
const id = (i: number) => `0192f1c4-0000-7000-8000-${String(i).padStart(12, '0')}`;
const many = (n: number): GeoCity[] =>
  Array.from({ length: n }, (_, i) => city({ id: id(i), slug: `c${i}`, name: `Город ${i}` }));

// сервер городов: страница по limit, курсор — позиция; ids — фильтр (до 100)
function citiesServer(all: GeoCity[]) {
  const seen: URLSearchParams[] = [];
  server.use(
    http.get('/v1/geo/cities', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.push(q);
      const ids = q.getAll('ids');
      if (ids.length > 100) return response.untyped(Response.json({}, { status: 400 }));
      const rows = ids.length > 0 ? all.filter((c) => ids.includes(c.id)) : all;
      const limit = Number(q.get('limit') ?? 20);
      const from = Number(q.get('cursor') ?? 0);
      const items = rows.slice(from, from + limit);
      return response(200).json({
        items,
        next_cursor: from + limit < rows.length ? String(from + limit) : null,
      });
    }),
  );
  return seen;
}

describe('useAllCities', () => {
  test('три страницы курсором по 100 — все города, без предупреждения', async () => {
    const seen = citiesServer(many(250));
    const { result } = renderHook(() => useAllCities({}, []), { wrapper: providers() });
    await waitFor(() => expect(result.current.cities).toHaveLength(250));
    expect(result.current.capped).toBe(false);
    expect(seen.map((q) => [q.get('limit'), q.get('cursor')])).toEqual([
      ['100', null],
      ['100', '100'],
      ['100', '200'],
    ]);
  });

  test(`потолок ${CITIES_CAP}: дальше не идёт, предупреждение`, async () => {
    const seen = citiesServer(many(CITIES_CAP + 150));
    const { result } = renderHook(() => useAllCities({}, []), { wrapper: providers() });
    await waitFor(() => expect(result.current.cities).toHaveLength(CITIES_CAP));
    expect(result.current.capped).toBe(true);
    expect(seen).toHaveLength(CITIES_CAP / 100);
  });

  test('города флага вне загруженных — догружены по ids пачками по 100', async () => {
    const all = many(CITIES_CAP + 300);
    const seen = citiesServer(all);
    // 150 городов флага за потолком и один из загруженных
    const flagIds = [id(5), ...all.slice(CITIES_CAP + 10, CITIES_CAP + 160).map((c) => c.id)];
    const { result } = renderHook(() => useAllCities({}, flagIds), { wrapper: providers() });
    await waitFor(() => expect(result.current.cities).toHaveLength(CITIES_CAP + 150));
    const byIds = seen.filter((q) => q.getAll('ids').length > 0);
    expect(byIds.map((q) => q.getAll('ids').length)).toEqual([100, 50]);
    expect(byIds.every((q) => !q.getAll('ids').includes(id(5)))).toBe(true);
    expect(result.current.cities.some((c) => c.id === all[CITIES_CAP + 159]?.id)).toBe(true);
  });

  test('поиск или статус — только города фильтра, без догрузки по ids', async () => {
    const seen = citiesServer(many(30));
    const { result } = renderHook(() => useAllCities({ q: 'Город 1' }, [id(99)]), {
      wrapper: providers(),
    });
    await waitFor(() => expect(result.current.cities.length).toBeGreaterThan(0));
    await waitFor(() => expect(result.current.isFetching).toBe(false));
    expect(seen.every((q) => q.getAll('ids').length === 0)).toBe(true);
    expect(seen[0]?.get('q')).toBe('Город 1');
  });
});
