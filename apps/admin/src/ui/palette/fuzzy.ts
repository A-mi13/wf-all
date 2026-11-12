// Нечёткий поиск палитры (спека UI-фундамента §4.1, план UF4.4) — своё, не библиотека: локально ищется ~50 строк
// (разделы, действия, недавнее, страны), сущности ищет сервер. Библиотеки раскладку не делают, остальное — ниже.
// - Сравнение без регистра, ё = е, NFKC (полноширинные знаки, лигатуры); составной «е + U+0308» — одна буква.
// - Раскладка: запрос, набранный не в той раскладке («ujhjl»), — второй вариант запроса («город»), чуть ниже прямого.
// - Ранжирование: с начала поля > с начала слова > подстрока > подпоследовательность (меньше разрывов — выше);
//   поле подписи (первое) выше ключевых слов; равные — в исходном порядке.
// - ranges — полуоткрытые [начало, конец) в индексах ИСХОДНОЙ строки поля field (для <mark>), соседние склеены.

export type FuzzyResult<T> = { item: T; score: number; ranges: [number, number][]; field: number };

const EN = "`qwertyuiop[]asdfghjkl;'zxcvbnm,.";
const RU = 'ёйцукенгшщзхъфывапролджэячсмитьбю';
const TO_RU = new Map([...EN].map((c, i) => [c, RU[i]!]));
const TO_EN = new Map([...RU].map((c, i) => [c, EN[i]!]));

// Строка в другой раскладке: латиница → кириллица по клавишам и обратно (регистр не важен — сравнение без него).
export function switchLayout(s: string): string {
  return [...s.toLowerCase()].map((c) => TO_RU.get(c) ?? TO_EN.get(c) ?? c).join('');
}

const foldUnit = (unit: string) => unit.normalize('NFKC').toLowerCase().replaceAll('ё', 'е');

// Нормализованная строка и для каждого её знака — диапазон исходной строки.
type Folded = { text: string; from: number[]; to: number[] };

function fold(s: string): Folded {
  let text = '';
  const from: number[] = [];
  const to: number[] = [];
  // буква с последующими диакритиками — одна единица (е + U+0308 → ё → е)
  for (const m of s.matchAll(/\P{M}\p{M}*|\p{M}+/gu)) {
    const unit = foldUnit(m[0]);
    for (let i = 0; i < unit.length; i++) {
      from.push(m.index);
      to.push(m.index + m[0].length);
    }
    text += unit;
  }
  return { text, from, to };
}

export function normalize(s: string): string {
  return fold(s).text;
}

const WORD = /[\p{L}\p{N}]/u;
const PREFIX = 400;
const WORD_START = 300;
const SUBSTRING = 200;
const SUBSEQUENCE = 100;
const FIELD_PENALTY = 20;
const LAYOUT_PENALTY = 5;

// Позиции совпавших знаков нормализованной строки → диапазоны исходной, соседние склеены.
function toRanges(f: Folded, positions: number[]): [number, number][] {
  const out: [number, number][] = [];
  for (const p of positions) {
    const start = f.from[p]!;
    const end = f.to[p]!;
    const last = out[out.length - 1];
    if (last && start <= last[1]) last[1] = Math.max(last[1], end);
    else out.push([start, end]);
  }
  return out;
}

const span = (start: number, length: number) => Array.from({ length }, (_, i) => start + i);

function matchField(q: string, f: Folded): { score: number; positions: number[] } | null {
  const text = f.text;
  if (text.startsWith(q)) return { score: PREFIX, positions: span(0, q.length) };
  let substring = -1;
  for (let i = text.indexOf(q); i !== -1; i = text.indexOf(q, i + 1)) {
    if (!WORD.test(text[i - 1] ?? '')) return { score: WORD_START, positions: span(i, q.length) };
    if (substring === -1) substring = i;
  }
  if (substring !== -1) return { score: SUBSTRING, positions: span(substring, q.length) };
  // подпоследовательность: жадно слева, штраф за разрывы
  const positions: number[] = [];
  let at = 0;
  for (const c of q) {
    const i = text.indexOf(c, at);
    if (i === -1) return null;
    positions.push(i);
    at = i + 1;
  }
  const gaps = positions.reduce((n, p, i) => n + (i > 0 && p !== positions[i - 1]! + 1 ? 1 : 0), 0);
  return { score: SUBSEQUENCE - Math.min(gaps * 5, 50), positions };
}

export function fuzzyRank<T>(
  query: string,
  items: readonly T[],
  opts: { fields: (item: T) => string[] },
): FuzzyResult<T>[] {
  const direct = normalize(query.trim());
  if (direct === '') return items.map((item) => ({ item, score: 0, ranges: [], field: 0 }));
  const switched = normalize(switchLayout(query.trim()));
  const variants: [string, number][] =
    switched === direct
      ? [[direct, 0]]
      : [
          [direct, 0],
          [switched, LAYOUT_PENALTY],
        ];

  const out: (FuzzyResult<T> & { index: number })[] = [];
  items.forEach((item, index) => {
    let best: FuzzyResult<T> | null = null;
    opts.fields(item).forEach((field, fi) => {
      const f = fold(field);
      for (const [q, penalty] of variants) {
        const m = matchField(q, f);
        if (!m) continue;
        const score = m.score - fi * FIELD_PENALTY - penalty;
        if (!best || score > best.score) {
          best = { item, score, ranges: toRanges(f, m.positions), field: fi };
        }
      }
    });
    if (best) out.push({ ...(best as FuzzyResult<T>), index });
  });
  out.sort((a, b) => b.score - a.score || a.index - b.index);
  return out.map(({ item, score, ranges, field }) => ({ item, score, ranges, field }));
}
