// Дата без времени (спека UI-фундамента §4.17): строка YYYY-MM-DD, арифметика — по частям через Date.UTC,
// форматирование — Intl с timeZone 'UTC'. new Date('YYYY-MM-DD') и местные геттеры не используются: полночь
// UTC в западных поясах — ещё вчера, и день съехал бы.
export type Ymd = { y: number; m: number; d: number }; // m — 1…12

const ISO = /^(\d{4})-(\d{2})-(\d{2})$/;
const pad = (n: number, w = 2) => String(n).padStart(w, '0');

export const toIso = ({ y, m, d }: Ymd): string => `${pad(y, 4)}-${pad(m)}-${pad(d)}`;

export const daysInMonth = (y: number, m: number): number =>
  new Date(Date.UTC(y, m, 0)).getUTCDate();

// Существующая дата из частей или null (31.02 — нет). Год — от 1000: Date.UTC переводит годы 0–99 в
// 1900-е (ревью UF2a/II-A, m6), а год формата языка — четыре цифры, ниже 1000 — опечатка.
export function ymd(y: number, m: number, d: number): Ymd | null {
  if (!Number.isInteger(y) || y < 1000 || y > 9999) return null;
  if (!Number.isInteger(m) || m < 1 || m > 12) return null;
  if (!Number.isInteger(d) || d < 1 || d > daysInMonth(y, m)) return null;
  return { y, m, d };
}

export function parseIso(s: string | undefined | null): Ymd | null {
  const r = ISO.exec(s ?? '');
  return r ? ymd(Number(r[1]), Number(r[2]), Number(r[3])) : null;
}

const fromUtc = (t: number): Ymd => {
  const dt = new Date(t);
  return { y: dt.getUTCFullYear(), m: dt.getUTCMonth() + 1, d: dt.getUTCDate() };
};
export const utc = ({ y, m, d }: Ymd): number => Date.UTC(y, m - 1, d);

export const addDays = (v: Ymd, n: number): Ymd => fromUtc(Date.UTC(v.y, v.m - 1, v.d + n));

// Месяц вперёд/назад; день — не дальше последнего дня нового месяца (31.01 → 28.02).
export function addMonths(v: Ymd, n: number): Ymd {
  const total = v.y * 12 + (v.m - 1) + n;
  const y = Math.floor(total / 12);
  const m = (total % 12) + 1;
  return { y, m, d: Math.min(v.d, daysInMonth(y, m)) };
}

// День недели по ISO: 1 — понедельник … 7 — воскресенье.
export const isoWeekday = (v: Ymd): number => ((new Date(utc(v)).getUTCDay() + 6) % 7) + 1;

export const compare = (a: Ymd, b: Ymd): number => utc(a) - utc(b);

// Сегодня — по часам устройства в его поясе (местные части, не UTC).
export function today(now = new Date()): Ymd {
  return { y: now.getFullYear(), m: now.getMonth() + 1, d: now.getDate() };
}

// Формат числовой даты языка: порядок частей и разделители из Intl#formatToParts (ru — ДД.ММ.ГГГГ,
// en — ММ/ДД/ГГГГ).
type Part = 'day' | 'month' | 'year';
const WIDTH: Record<Part, number> = { day: 2, month: 2, year: 4 };
export function dateFormat(locale: string) {
  const fmt = new Intl.DateTimeFormat(locale, {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    timeZone: 'UTC',
  });
  const order: Part[] = [];
  const seps: string[] = []; // seps[i] — разделитель после части order[i]
  for (const p of fmt.formatToParts(Date.UTC(2026, 10, 22))) {
    if (p.type === 'day' || p.type === 'month' || p.type === 'year') order.push(p.type);
    else if (p.type === 'literal' && order.length > seps.length) seps.push(p.value);
  }
  const last = order.length - 1;
  return {
    format: (v: Ymd): string => fmt.format(utc(v)),
    // Маска ввода (спека §4.17; ревью UF2a/II-A, I3): цифровая клавиатура iOS (inputMode numeric) не даёт
    // «.», «/» и пробела — разделитель языка подставляется, когда часть набрана целиком и пошла следующая
    // цифра (не заранее: иначе Backspace по разделителю тут же вернул бы его). Свой разделитель (любой
    // не-цифровой знак) закрывает недописанную часть: «1.» — день 1. Лишнее отбрасывается. ISO YYYY-MM-DD
    // (вставка из адреса) остаётся как есть — parse его понимает.
    mask(text: string): string {
      if (ISO.test(text.trim())) return text.trim();
      let out = '';
      let part = 0;
      let count = 0;
      const chars = [...text];
      for (const [i, ch] of chars.entries()) {
        const type = order[part];
        if (!type) break;
        if (ch >= '0' && ch <= '9') {
          if (count >= WIDTH[type]) {
            if (part === last) break;
            // Вставка в середину заполненной даты — любой длины (ревью UF2a/II r1, раунд 1 IL2-фронта): если после
            // этих цифр текст сам отделяет разделителями все следующие части («0112.10.2026», «051.10.2026»), перенос
            // сдвинул бы цифры через разделители в годную, но чужую дату (01.12.1020). Часть остаётся переполненной —
            // текст виден как введён, parse его отвергнет (ошибка формата у поля), ввод не теряется. Набор подряд
            // (цифры до конца текста) и вставка, где разделителей на следующие части не хватает («0110.2026»,
            // «01102026»), переносятся, как раньше.
            let groups = 0; // разделитель → цифра после текущих цифр: сколько следующих частей текст отделил сам
            for (let j = i + 1; j < chars.length; j++) {
              const digit = /\d/.test(chars[j] ?? '');
              if (digit && !/\d/.test(chars[j - 1] ?? '')) groups += 1;
            }
            if (groups >= last - part) {
              out += ch;
              count += 1;
              continue;
            }
            out += seps[part] ?? '';
            part += 1;
            count = 0;
          }
          out += ch;
          count += 1;
        } else if (count > 0 && part < last) {
          out += seps[part] ?? '';
          part += 1;
          count = 0;
        }
      }
      return out;
    },
    // Текст пользователя → дата: три группы цифр в порядке языка (разделитель — любой не-цифровой знак),
    // 8 цифр подряд (ДДММГГГГ / ММДДГГГГ — вставка без разделителей) или ISO YYYY-MM-DD (вставка из
    // адреса, журнала). Год — четыре цифры.
    parse(text: string): Ymd | null {
      const s = text.trim();
      if (ISO.test(s)) return parseIso(s);
      let at = 0;
      const groups = /^\d{8}$/.test(s)
        ? order.map((type) => s.slice(at, (at += WIDTH[type])))
        : s.match(/\d+/g);
      if (!groups || groups.length !== 3 || /[^\d\s./-]/.test(s)) return null;
      const parts: Record<Part, number> = { day: 0, month: 0, year: 0 };
      for (const [i, type] of order.entries()) {
        const g = groups[i] ?? '';
        if (type === 'year' && g.length !== 4) return null;
        if (type !== 'year' && g.length > 2) return null;
        parts[type] = Number(g);
      }
      return ymd(parts.year, parts.month, parts.day);
    },
  };
}
