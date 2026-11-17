import { describe, expect, test } from 'vitest';
import { cleanAccessReason, reasonProblem } from './reason';

// Причина доступа к ПД (заголовок X-WF-Access-Reason): сервер отвергает управляющие и невидимые символы (422
// format). Клиент срезает безопасные невидимые (мягкий перенос, нулевой ширины, LRM/RLM, BOM) и отказывает на
// остальное до запроса; длина 3–200 — в символах после чистки.
describe('cleanAccessReason', () => {
  test('срезает U+00AD, U+200B–U+200F, U+FEFF и пробелы по краям', () => {
    expect(cleanAccessReason(' об\u00ADра\u200Bще\u200Dние\u200E\u200F 4812\uFEFF\u200C ')).toBe(
      'обращение 4812',
    );
  });
});

describe('reasonProblem', () => {
  test('годная причина — без ошибки', () => {
    expect(reasonProblem('обращение 4812')).toBeNull();
    expect(reasonProblem('abc')).toBeNull();
    // символы, а не единицы UTF-16: 200 мячей — ровно предел
    expect(reasonProblem('⚽'.repeat(200))).toBeNull();
    expect(reasonProblem('\u{1f3c6}'.repeat(200))).toBeNull();
  });

  test('короче 3 и длиннее 200 символов', () => {
    expect(reasonProblem('ab')).toBe('minLength');
    expect(reasonProblem('a'.repeat(201))).toBe('maxLength');
  });

  test.each([
    ['перевод строки', 'обра\nщение'],
    ['табуляция', 'обра\tщение'],
    ['bidi-переопределение', 'обра\u202Eщение'],
    ['изоляция направления', 'обра\u2066щение'],
    ['разделитель строк', 'обра\u2028щение'],
    ['NUL', 'обра\u0000щение'],
    ['DEL', 'обра\u007fщение'],
  ])('%s — format', (_, v) => {
    expect(reasonProblem(v)).toBe('format');
  });
});
