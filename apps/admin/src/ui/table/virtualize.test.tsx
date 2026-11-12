import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, test } from 'vitest';
import { stubTableLayout } from '../../test/layout';
import { renderWithProviders } from '../../test/render';
import { DataTable } from './DataTable';
import type { Column } from './model';
import { forgetTablePositions, rememberRow, restoreKeyOf } from './restore';

// Возврат на ту же позицию и виртуализация строк (спека UI-фундамента §4.5, план UF2b.4). jsdom не
// раскладывает: окно прокрутки — 400 px, строка — 40 px (src/test/layout.ts).

type R = { id: string; name: string };
const make = (n: number, from = 0): R[] =>
  Array.from({ length: n }, (_, i) => ({ id: `r${i + from}`, name: `Строка ${i + from}` }));
const cols: Column<R>[] = [{ id: 'name', header: 'Имя', cell: (r) => r.name }];

let restore: () => void;
beforeEach(() => {
  restore = stubTableLayout();
});
afterEach(() => {
  restore();
  forgetTablePositions();
});

function Table(props: {
  rows: R[];
  virtualize?: boolean;
  restoreKey?: string;
  onRowClick?: (r: R) => void;
}) {
  return (
    <DataTable label="Длинная" columns={cols} rowKey={(r) => r.id} onRetry={() => {}} {...props} />
  );
}

const table = () => screen.getByRole('table', { name: 'Длинная' });
const bodyRows = () => within(table()).getAllByRole('row').slice(1);

describe('виртуализация строк', () => {
  test('300 строк → в DOM не все, aria-rowcount и aria-rowindex (шапка — строка 1)', () => {
    renderWithProviders(<Table rows={make(300)} />);
    expect(bodyRows().length).toBeGreaterThan(0);
    expect(bodyRows().length).toBeLessThan(40);
    expect(table()).toHaveAttribute('aria-rowcount', '301');
    const [header, first, second] = within(table()).getAllByRole('row');
    expect(header).toHaveAttribute('aria-rowindex', '1');
    expect(first).toHaveAttribute('aria-rowindex', '2');
    expect(first).toHaveTextContent('Строка 0');
    expect(second).toHaveAttribute('aria-rowindex', '3');
    expect(table()).not.toHaveTextContent('Строка 299');
  });

  test('порог: 200 строк — все в DOM без aria-rowcount; virtualize={false} при 300 — все; true при 50 — окно', () => {
    const { rerender } = renderWithProviders(<Table rows={make(200)} />);
    expect(bodyRows()).toHaveLength(200);
    expect(table()).not.toHaveAttribute('aria-rowcount');
    rerender(<Table rows={make(300)} virtualize={false} />);
    expect(bodyRows()).toHaveLength(300);
    rerender(<Table rows={make(50)} virtualize />);
    expect(bodyRows().length).toBeLessThan(50);
    expect(table()).toHaveAttribute('aria-rowcount', '51');
  });

  test('перечитывание не сдвигает строк: фокус остаётся на строке по ключу (данные заменены целиком)', () => {
    const rows = make(300);
    const { rerender } = renderWithProviders(<Table rows={rows} onRowClick={() => {}} />);
    const row5 = screen.getByRole('row', { name: /Строка 5$/ });
    act(() => row5.focus());
    // новая строка сверху, все объекты новые
    rerender(
      <Table
        rows={[{ id: 'new', name: 'Новая' }, ...rows.map((r) => ({ ...r }))]}
        onRowClick={() => {}}
      />,
    );
    expect(document.activeElement).toBe(row5);
    expect(row5).toHaveTextContent('Строка 5');
  });
});

describe('select + виртуализация', () => {
  function Picker() {
    const [value, setValue] = useState<string | null>('r0');
    return (
      <DataTable
        label="Места"
        columns={[]}
        rowKey={(r: R) => r.id}
        rows={make(300)}
        onRetry={() => {}}
        select={{ value, onChange: (r) => setValue(r.id), option: (r) => r.name }}
      />
    );
  }

  test('End и ↑ к строке вне окна → она в DOM, aria-activedescendant — существующий id', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Picker />);
    const list = screen.getByRole('listbox', { name: 'Места' });
    expect(within(list).getAllByRole('option').length).toBeLessThan(40);
    list.focus();
    await user.keyboard('{End}');
    const id = list.getAttribute('aria-activedescendant');
    expect(id).toBeTruthy();
    expect(document.getElementById(id ?? '')).toHaveTextContent('Строка 299');
    await user.keyboard('{Home}');
    expect(
      document.getElementById(list.getAttribute('aria-activedescendant') ?? ''),
    ).toHaveTextContent('Строка 0');
  });
});

describe('возврат на позицию', () => {
  test('ключ позиции — путь и search без ключей панели; порядок search не важен', () => {
    expect(restoreKeyOf({ pathname: '/staff', search: {} })).toBe('/staff');
    // ключи панелей (PANEL_SEARCH_KEYS, UF2b.8) — вне ключа позиции, фильтры (в том числе country) — в нём
    expect(restoreKeyOf({ pathname: '/staff', search: { q: 'a', account: 'x', invite: 1 } })).toBe(
      restoreKeyOf({ pathname: '/staff', search: { q: 'a' } }),
    );
    expect(restoreKeyOf({ pathname: '/geo/cities', search: { country: 'RU' } })).not.toBe(
      '/geo/cities',
    );
    expect(restoreKeyOf({ pathname: '/staff', search: { q: 'a', role: 'admin' } })).toBe(
      restoreKeyOf({ pathname: '/staff', search: { role: 'admin', q: 'a' } }),
    );
    expect(restoreKeyOf({ pathname: '/staff', search: { q: 'a' } })).not.toBe(
      restoreKeyOf({ pathname: '/staff', search: { q: 'b' } }),
    );
  });

  test('контейнер — data-scroll-restoration-id без текста адреса (поиск не уходит в storage роутера)', () => {
    renderWithProviders(<Table rows={make(3)} restoreKey='/staff?[["q","ivan@wf.example"]]' />);
    const id = table()
      .closest('[data-scroll-restoration-id]')
      ?.getAttribute('data-scroll-restoration-id');
    expect(id).toMatch(/^wf-table-[0-9a-z]+$/);
  });

  test('ушли со строки (щелчок) — при следующем монтировании с тем же ключом фокус на ней', async () => {
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(
      <Table rows={make(10)} restoreKey="k" onRowClick={() => {}} />,
    );
    await user.click(screen.getByRole('row', { name: /Строка 7$/ }));
    unmount();
    expect(document.activeElement).toBe(document.body);
    renderWithProviders(<Table rows={make(10)} restoreKey="k" onRowClick={() => {}} />);
    expect(document.activeElement).toHaveTextContent('Строка 7');
  });

  test('виртуально: строка вне окна — прокрутка к ней и фокус', async () => {
    rememberRow('k', 'r250');
    renderWithProviders(<Table rows={make(300)} restoreKey="k" onRowClick={() => {}} />);
    await waitFor(() => expect(document.activeElement).toHaveTextContent('Строка 250'));
    expect(document.activeElement).toHaveAttribute('aria-rowindex', '252');
  });

  test('фокус не потерян (перешли меню) — не перехватываем; другой ключ — не трогаем', () => {
    rememberRow('k', 'r3');
    const outside = document.createElement('button');
    document.body.append(outside);
    outside.focus();
    renderWithProviders(<Table rows={make(10)} restoreKey="k" onRowClick={() => {}} />);
    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  test('строки, с которой ушли, больше нет — фокус не ставится; forgetTablePositions стирает память', () => {
    rememberRow('k', 'gone');
    const { unmount } = renderWithProviders(
      <Table rows={make(10)} restoreKey="k" onRowClick={() => {}} />,
    );
    expect(document.activeElement).toBe(document.body);
    unmount();
    rememberRow('k', 'r2');
    forgetTablePositions();
    renderWithProviders(<Table rows={make(10)} restoreKey="k" onRowClick={() => {}} />);
    expect(document.activeElement).toBe(document.body);
  });
});
