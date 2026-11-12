/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, test } from 'vitest';
import { KEYS as APPROVALS_KEYS, parseApprovalsSearch } from '../features/approvals/search';
import { KEYS as AUDIT_KEYS, parseAuditSearch } from '../features/audit/search';
import { KEYS as CITIES_KEYS, parseCitiesSearch } from '../features/geo/search';
import { KEYS as STAFF_KEYS, parseStaffSearch } from '../features/staff/search';
import { FILTER_SEARCH_KEYS } from './filterKeys';
import { AUTH_PATHS, PANEL_SEARCH_KEYS, paths } from './paths';
import { routeTree } from './routes';
import { createRouter } from '@tanstack/react-router';
import { parseKeys } from './search';

// Страж I1 (спека UI-фундамента §4.6, §15; план UF2b.8): ключ панели не совпадает ни с одним ключом фильтра ни на
// одном пути и ни с одним query-параметром списочных операций контракта admin — иначе BackMemory и ключ позиции
// таблицы вырезали бы фильтр (панель страны — ?edit=, а country — фильтр). Ключи фильтров объявлены рядом с
// парсером (KEYS), парсер возвращает только их (n7).

// Имена query-параметров операций контракта — из сгенерированного admin.d.ts текстом: блоки «query?: {…}».
function contractQueryParams(): string[] {
  const lines = readFileSync(join(import.meta.dirname, '../api/gen/admin.d.ts'), 'utf8').split(
    '\n',
  );
  const names = new Set<string>();
  for (let i = 0; i < lines.length; i++) {
    const open = /^(\s*)query\??: \{$/.exec(lines[i] ?? '');
    if (!open) continue;
    const indent = (open[1] ?? '').length;
    const field = new RegExp(`^\\s{${indent + 4}}"?([\\w-]+)"?\\??:`);
    for (i++; i < lines.length && !new RegExp(`^\\s{${indent}}\\};?$`).test(lines[i] ?? ''); i++) {
      const m = field.exec(lines[i] ?? '');
      if (m?.[1]) names.add(m[1]);
    }
  }
  return [...names];
}

describe('ключи панелей и фильтров', () => {
  test('реестр панелей — по спеке §4.6', () => {
    expect(PANEL_SEARCH_KEYS).toEqual([
      'account',
      'invite',
      'activate',
      'entry',
      'edit',
      'start',
      'import',
    ]);
  });

  test('фильтры путей собраны из KEYS парсеров', () => {
    expect(FILTER_SEARCH_KEYS).toEqual({
      [paths.audit]: ['tab', 'action', 'actor', 'object_type', 'object_id', 'from', 'to'],
      [paths.staff]: ['q', 'status', 'role'],
      [paths.approvals]: ['box'],
      [paths.cities]: ['q', 'status', 'country', 'region_id'],
    });
  });

  test('ключ панели ∩ ключи фильтров всех путей = ∅', () => {
    const filters = new Set(Object.values(FILTER_SEARCH_KEYS).flat());
    expect(PANEL_SEARCH_KEYS.filter((k) => filters.has(k))).toEqual([]);
  });

  test('ключ панели ∩ query-параметры контракта admin = ∅', () => {
    const params = contractQueryParams();
    // стражу есть что читать: параметры списков сотрудников, журнала, городов, импорта
    expect(params).toEqual(
      expect.arrayContaining(['status', 'role', 'country', 'action_prefix', 'cursor']),
    );
    expect(PANEL_SEARCH_KEYS.filter((k) => params.includes(k))).toEqual([]);
  });
});

describe('парсер возвращает только свои KEYS (n7)', () => {
  // все ключи с годными значениями и посторонний — в ответе только KEYS
  const cases = [
    {
      name: '/audit',
      keys: AUDIT_KEYS,
      parse: parseAuditSearch,
      input: {
        tab: 'staff',
        action: 'staff.invite_created',
        actor: '0192f1c4-0000-7b21-9c5d-0000000000ad',
        object_type: 'staff_account',
        object_id: '0192f1c4-0000-7b21-9c5d-0000000000ad',
        from: '2026-10-01',
        to: '2026-10-02',
      },
    },
    {
      name: '/staff',
      keys: STAFF_KEYS,
      parse: parseStaffSearch,
      input: { q: 'мар', status: 'active', role: 'admin' },
    },
    {
      name: '/approvals',
      keys: APPROVALS_KEYS,
      parse: parseApprovalsSearch,
      input: { box: 'mine' },
    },
    {
      name: '/geo/cities',
      keys: CITIES_KEYS,
      parse: parseCitiesSearch,
      input: {
        q: 'ст',
        status: ['live'],
        country: 'RU',
        region_id: '0192f1c4-0000-7b21-9c5d-0000000000ad',
      },
    },
  ] as const;

  test.each(cases)('$name', ({ keys, parse, input }) => {
    const out = parse({ ...input, invite: 1, account: 'x', foreign: 'y' } as Record<
      string,
      unknown
    >);
    expect(Object.keys(out).sort()).toEqual([...keys].sort());
  });

  test('тип: лишний ключ результата не компилируется', () => {
    const keys = ['a'] as const;
    // @ts-expect-error — b нет в KEYS
    const out = parseKeys<'a', { a?: string; b?: string }>(keys, { a: (v) => String(v) }, { a: 1 });
    expect(out).toEqual({ a: '1' });
  });
});

// Ревью UF2b/II-B, m3: страж видит маршруты из дерева роутера, а не из списка — новый маршрут с validateSearch
// (G9: /geo/imports) без строки в FILTER_SEARCH_KEYS краснеет; ключи, которые маршрут оставляет в search, — только
// его фильтры и ключи панелей.
describe('маршруты с search-параметрами — в FILTER_SEARCH_KEYS', () => {
  type Node = {
    fullPath: string;
    options: { validateSearch?: (s: Record<string, unknown>) => object };
    children?: Node[] | Record<string, Node>;
  };
  const walk = (node: Node): Node[] => {
    const kids = node.children ? Object.values(node.children) : [];
    return [node, ...kids.flatMap(walk)];
  };
  // fullPath маршрута вычисляет роутер при создании
  createRouter({
    routeTree,
    context: {
      session: { status: 'loading', refresh: async () => {}, afterLogout: async () => {} },
    },
  });
  const searched = walk(routeTree as unknown as Node).filter(
    (r) => r.options.validateSearch && !AUTH_PATHS.includes(r.fullPath),
  );

  test('стражу есть что обходить', () => {
    expect(searched.map((r) => r.fullPath).sort()).toEqual(
      [paths.approvals, paths.audit, paths.cities, paths.staff].sort(),
    );
  });

  test.each(searched.map((r) => [r.fullPath, r] as const))('%s', (path, route) => {
    const filters = FILTER_SEARCH_KEYS[path];
    expect(filters).toBeDefined();
    const out = route.options.validateSearch?.({ foreign: 'x', country_code: 'RU' }) ?? {};
    const allowed = new Set<string>([...(filters ?? []), ...PANEL_SEARCH_KEYS]);
    expect(Object.keys(out).filter((k) => !allowed.has(k))).toEqual([]);
  });
});
