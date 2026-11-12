// Разбор search-параметров экрана по списку ключей (план UF2b.8, n7): ключи фильтров объявлены рядом с парсером
// (KEYS в features/*/search.ts), парсер обходит только их — ключ вне списка в результат не попадёт по построению,
// а тип результата с ключом вне KEYS не скомпилируется. Порядок KEYS — порядок разбора: разбор ключа видит уже
// разобранные (действие — после вкладки, «по» — после «с», регион — после страны).
type OnlyKeys<T, K extends string> = { [P in keyof T]: P extends K ? unknown : never };

export type KeyParsers<K extends string, T> = {
  [P in K]: (value: unknown, parsed: Readonly<T>) => (P extends keyof T ? T[P] : never) | undefined;
};

export function parseKeys<K extends string, T extends OnlyKeys<T, K>>(
  keys: readonly K[],
  parsers: KeyParsers<K, T>,
  search: Record<string, unknown>,
): T {
  const out: Record<string, unknown> = {};
  for (const k of keys) {
    const v = parsers[k](search[k], out as T);
    if (v !== undefined) out[k] = v;
  }
  return out as T;
}
