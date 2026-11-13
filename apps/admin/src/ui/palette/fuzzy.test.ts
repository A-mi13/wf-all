import { describe, expect, test } from 'vitest';
import { fuzzyRank, normalize, switchLayout } from './fuzzy';

// Нечёткий поиск палитры (спека UI-фундамента §4.1, план UF4.4): регистр, ё→е, NFKC, раскладка ru↔en, английские
// ключевые слова, ранжирование «с начала > с начала слова > подстрока > подпоследовательность», точные диапазоны.

type Item = { label: string; keywords?: string };
const fields = (i: Item) => (i.keywords ? [i.label, i.keywords] : [i.label]);
const rank = (query: string, items: Item[]) =>
  fuzzyRank(query, items, { fields }).map((r) => r.item.label);

describe('fuzzyRank', () => {
  test.each<[string, string, Item[], string[]]>([
    [
      '«гор» — с начала выше середины слова; без совпадения — нет',
      'гор',
      [{ label: 'Импорт GeoNames' }, { label: 'Пригород' }, { label: 'Города и запуск' }],
      ['Города и запуск', 'Пригород'],
    ],
    [
      'с начала слова выше подстроки',
      'зап',
      [{ label: 'Перезапуск' }, { label: 'Города и запуск' }],
      ['Города и запуск', 'Перезапуск'],
    ],
    ['раскладка en→ru: «ujhjl» — «город»', 'ujhjl', [{ label: 'Город' }], ['Город']],
    ['раскладка ru→en: «ыефаа» — «staff»', 'ыефаа', [{ label: 'staff' }], ['staff']],
    ['«ёлка» находит «елка»', 'ёлка', [{ label: 'елка' }], ['елка']],
    ['«елка» находит «Ёлка»', 'елка', [{ label: 'Ёлка' }], ['Ёлка']],
    [
      '«city» при русском интерфейсе — по ключевым словам',
      'city',
      [{ label: 'Журнал действий' }, { label: 'Города и запуск', keywords: 'город city launch' }],
      ['Города и запуск'],
    ],
    [
      'подпоследовательность «жрн» — ниже совпадения с начала',
      'жрн',
      [{ label: 'Журнал действий' }, { label: 'Жрн-отчёт' }],
      ['Жрн-отчёт', 'Журнал действий'],
    ],
    [
      'совпадение в подписи выше, чем в ключевых словах',
      'стр',
      [{ label: 'Флаги', keywords: 'страна' }, { label: 'Страны' }],
      ['Страны', 'Флаги'],
    ],
    [
      'NFKC: полноширинные латинские — как обычные',
      'ＧＥＯ',
      [{ label: 'GeoNames' }],
      ['GeoNames'],
    ],
  ])('%s', (_, query, items, expected) => {
    expect(rank(query, items)).toEqual(expected);
  });

  test('пустой запрос и пробелы — исходный порядок, без диапазонов', () => {
    const items = [{ label: 'Б' }, { label: 'А' }, { label: 'В' }];
    for (const q of ['', '   ']) {
      const out = fuzzyRank(q, items, { fields });
      expect(out.map((r) => r.item.label)).toEqual(['Б', 'А', 'В']);
      expect(out.every((r) => r.ranges.length === 0)).toBe(true);
    }
  });

  test('диапазоны точные, полуоткрытые, в индексах исходной строки', () => {
    const [prefix] = fuzzyRank('гор', [{ label: 'Города' }], { fields });
    expect(prefix?.ranges).toEqual([[0, 3]]);
    const [word] = fuzzyRank('зап', [{ label: 'Города и запуск' }], { fields });
    expect(word?.ranges).toEqual([[9, 12]]);
    const [sub] = fuzzyRank('жрн', [{ label: 'Журнал' }], { fields });
    // Ж(0) у р(2) н(3): соседние совпадения склеены
    expect(sub?.ranges).toEqual([
      [0, 1],
      [2, 4],
    ]);
    // ё в исходнике: индексы — исходной строки
    const [yo] = fuzzyRank('елк', [{ label: 'Ёлка' }], { fields });
    expect(yo?.ranges).toEqual([[0, 3]]);
    // составной ё (е + U+0308) — два знака исходника на одну букву
    const [composed] = fuzzyRank('ел', [{ label: 'ёлка' }], { fields });
    expect(composed?.ranges).toEqual([[0, 3]]);
    // совпадение по ключевым словам — диапазоны этого поля, field = 1
    const [kw] = fuzzyRank('city', [{ label: 'Города', keywords: 'город city' }], { fields });
    expect(kw).toMatchObject({ field: 1, ranges: [[6, 10]] });
  });

  test('нормализация и раскладка', () => {
    expect(normalize('ЁЛКА')).toBe('елка');
    expect(switchLayout('ujhjl')).toBe('город');
    expect(switchLayout('город')).toBe('ujhjl');
  });
});
