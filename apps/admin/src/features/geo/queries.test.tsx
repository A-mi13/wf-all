import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { geoServer } from '../../test/geo';
import { meOwner } from '../../test/me';
import { providers } from '../../test/render';
import { useGeoCities, useGeoPlaces, useGeoRegions } from './queries';

// Чтения «Справочников» на моке по контракту: массив status уходит повторённым параметром (explode,
// status=pilot&status=live — ловушка Task 7), язык названий — Accept-Language интерфейса.

describe('queries geo', () => {
  test('useGeoCities: status — повторённый параметр, фильтры и язык в запросе', async () => {
    const { seen } = geoServer();
    const { result } = renderHook(
      () => useGeoCities({ status: ['pilot', 'live'], country: 'RU', q: 'пят' }),
      { wrapper: providers({ me: { ...meOwner, locale: 'en' } }) },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const q = seen.cities.at(-1);
    expect(q?.getAll('status')).toEqual(['pilot', 'live']);
    expect(q?.get('country')).toBe('RU');
    expect(q?.get('q')).toBe('пят');
    expect(seen.languages.at(-1)).toBe('en');
    expect(result.current.data?.pages[0]?.items.map((c) => c.name)).toEqual(['Пятигорск']);
  });

  test('useGeoCities: «Показать ещё» — курсор следующей страницы', async () => {
    const { seen } = geoServer({ pageSize: 1 });
    const { result } = renderHook(() => useGeoCities({}), { wrapper: providers() });
    await waitFor(() => expect(result.current.hasNextPage).toBe(true));
    await result.current.fetchNextPage();
    expect(seen.cities.at(-1)?.get('cursor')).toBe('1');
  });

  test('useGeoRegions: без страны — запроса нет; со страной — регионы', async () => {
    const { seen } = geoServer();
    const { result, rerender } = renderHook(({ code }) => useGeoRegions(code), {
      wrapper: providers(),
      initialProps: { code: undefined as string | undefined },
    });
    expect(result.current.fetchStatus).toBe('idle');
    rerender({ code: 'RU' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(seen.regions).toEqual(['RU']);
  });

  test('useGeoPlaces: enabled=false — не ищет', () => {
    const { seen } = geoServer();
    renderHook(() => useGeoPlaces({ q: 'м' }, false), { wrapper: providers() });
    expect(seen.places).toEqual([]);
  });
});
